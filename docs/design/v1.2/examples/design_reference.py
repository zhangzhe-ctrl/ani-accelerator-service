"""Executable design model, not a GPU provider, quota service or production DB.

Run: python3 examples/design_reference.py
Integer MiB is the platform accounting unit. Provider blocks are translated once.
"""
from __future__ import annotations
from dataclasses import dataclass, asdict
from hashlib import sha256
from typing import Iterable
import json

MAX_I64 = 2**63 - 1
MAX_I32 = 2**31 - 1
MIB = 2**20


def natural(value: int, name: str, *, positive: bool = False, maximum: int = MAX_I64) -> int:
    if type(value) is not int or value < (1 if positive else 0) or value > maximum:
        raise ValueError(f'{name}: invalid integer/range')
    return value


def checked_product(*values: int) -> int:
    product = 1
    for value in values:
        natural(value, 'factor', positive=True)
        if product > MAX_I64 // value:
            raise ValueError('INT64_OVERFLOW')
        product *= value
    return product


def encode_shared(memory_mib: int, factor: int) -> int:
    natural(memory_mib, 'memory_mib', positive=True)
    natural(factor, 'memory_block_mib', positive=True, maximum=MAX_I32)
    if memory_mib % factor:
        raise ValueError('MEMORY_GRANULARITY_MISMATCH')
    blocks = memory_mib // factor
    # The researched v1.12.0 path multiplies int32 Usedmem by int32(F).
    if blocks > MAX_I32 or memory_mib > MAX_I32:
        raise ValueError('PROVIDER_INT32_RANGE')
    return blocks


def reported_capacity(physical_bytes: Iterable[int], factor: int) -> dict:
    natural(factor, 'factor', positive=True, maximum=MAX_I32)
    per_device = []
    for value in physical_bytes:
        total_mib = natural(value, 'physical_bytes') // MIB
        blocks = total_mib // factor
        per_device.append({'physical_mib': total_mib, 'blocks': blocks,
                           'reported_mib': blocks * factor,
                           'tail_mib': total_mib - blocks * factor})
    return {'per_device': per_device,
            'blocks': sum(x['blocks'] for x in per_device),
            'reported_mib': sum(x['reported_mib'] for x in per_device)}


def validate_group_factors(factors: Iterable[int]) -> int:
    values = list(factors)
    if not values:
        raise ValueError('MEMORY_UNIT_UNVERIFIED')
    for value in values:
        natural(value, 'factor', positive=True, maximum=MAX_I32)
    if len(set(values)) != 1:
        raise ValueError('FACTOR_MISMATCH')
    return values[0]


def quota_vector(mode: str, replicas: int, devices: int, memory_mib: int) -> dict[str, int]:
    natural(replicas, 'replicas', positive=True, maximum=256)
    if devices != 1:
        raise ValueError('SHAPE_UNSUPPORTED')
    units = checked_product(replicas, devices)
    if mode == 'WHOLE_EXCLUSIVE':
        if memory_mib != 0:
            raise ValueError('whole must not charge shared memory')
        return {'gpu.physical.count': units}
    if mode == 'SHARED_FIXED':
        return {'gpu.shared_memory_mib': checked_product(units, memory_mib)}
    raise ValueError('unknown mode')


def released_total(original: int, stored: int, incoming: int) -> tuple[int, int]:
    natural(original, 'original', positive=True)
    natural(stored, 'stored'); natural(incoming, 'incoming')
    if max(stored, incoming) > original:
        raise ValueError('QUOTA_RELEASE_CONFLICT')
    latest = max(stored, incoming)
    return latest, latest - stored


def can_cancel_unsent(state: str, dispatch_attempts: int) -> bool:
    natural(dispatch_attempts, 'dispatch_attempts')
    return state == 'QUEUED' and dispatch_attempts == 0


def desired_usage_revision(state: str, charges: Iterable[tuple[int, int]]) -> int:
    charges = list(charges)
    if not charges:
        raise ValueError('MISSING_ORIGINAL_CHARGES')
    for original, released in charges:
        released_total(original, released, released)
    fully_released = all(original == released for original, released in charges)
    if state == 'CANCELED_UNSENT' and not fully_released:
        raise ValueError('CANCELED_WITH_UNRELEASED_CHARGE')
    return 2 if fully_released else 1


def digest(value: object) -> str:
    encoded = json.dumps(value, sort_keys=True, ensure_ascii=False,
                         separators=(',', ':'), allow_nan=False).encode()
    return sha256(encoded).hexdigest()


@dataclass(frozen=True)
class UsageMessage:
    ref: tuple[str, str, str, str]  # tenant, owner, resource, create op
    revision: int
    plan_digest: str
    end_reason: str
    source_fact_ref: str
    source_operation_created_at: str

    def validate(self) -> None:
        if not all(self.ref) or self.revision not in (1, 2) or not self.plan_digest:
            raise ValueError('INVALID_USAGE')
        valid_reason = self.end_reason in ('GOVERNANCE_CANCELED_UNSENT', 'OWNER_RESOURCE_RELEASED')
        if (self.revision == 1 and self.end_reason) or (self.revision == 2 and not valid_reason):
            raise ValueError('INVALID_END_REASON')
        if not self.source_fact_ref or not self.source_operation_created_at:
            raise ValueError('MISSING_SOURCE')


class UsageProjection:
    """Sequential pure model of a row-locked upsert. Does not prove DB concurrency."""
    def __init__(self) -> None:
        self.rows: dict[tuple[str, str, str], UsageMessage] = {}

    def sync(self, incoming: UsageMessage) -> tuple[int, bool]:
        incoming.validate()
        key = incoming.ref[:3]
        old = self.rows.get(key)
        if old:
            if (old.ref != incoming.ref or old.plan_digest != incoming.plan_digest
                    or old.source_operation_created_at != incoming.source_operation_created_at):
                raise ValueError('USAGE_PROJECTION_CONFLICT')
            if incoming.revision == old.revision:
                if old != incoming:
                    raise ValueError('USAGE_PROJECTION_CONFLICT')
                return old.revision, True
            if incoming.revision < old.revision:
                return old.revision, True
        self.rows[key] = incoming
        return incoming.revision, False


@dataclass(frozen=True)
class Evidence:
    fact_id: str
    cluster: str
    device: str
    pod_uid: str
    container: str
    source: str
    value: int
    unit: str
    factor: int
    state: str
    core: int = 25

    def normalized_mib(self) -> int:
        natural(self.value, 'source value')
        if self.unit == 'BLOCKS':
            natural(self.factor, 'source factor', positive=True, maximum=MAX_I32)
            if self.value > MAX_I64 // self.factor:
                raise ValueError('INT64_OVERFLOW')
            return self.value * self.factor
        if self.unit == 'MIB':
            return self.value
        if self.unit == 'BYTES':
            # Actual utilization is not a claim. This input represents a promised runtime limit.
            if self.value % MIB:
                raise ValueError('UNALIGNED_CLAIM_BYTES')
            return self.value // MIB
        raise ValueError('UNKNOWN_SOURCE_UNIT')


@dataclass(frozen=True)
class Device:
    cluster: str
    device: str
    mode: str
    budget_mib: int
    slots: int
    eligible: bool = True
    additive_core_budget: int | None = 100


def effective_claims(evidence: Iterable[Evidence], retired_fact_ids: frozenset[str] = frozenset()) -> dict:
    """Adapter supplies explicitly retired facts after validated positive release evidence.
    Passing IDs is an algorithm input, NOT a release-proof API or authorization mechanism.
    A process-exit/missing-sample event alone must never generate retired_fact_ids.
    """
    grouped: dict[tuple, tuple[int, int]] = {}
    for e in evidence:
        if e.fact_id in retired_fact_ids or e.state == 'RELEASE_CONFIRMED':
            continue
        if e.state not in ('ACTIVE', 'UNKNOWN'):
            raise ValueError('INVALID_ALLOCATION_STATE')
        key = (e.cluster, e.device, e.pod_uid, e.container)
        vector = (e.normalized_mib(), natural(e.core, 'core', maximum=100))
        if key in grouped and grouped[key] != vector:
            raise ValueError('BINDING_EVIDENCE_CONFLICT')
        grouped[key] = vector
    return grouped


def device_capacity(device: Device, evidence: Iterable[Evidence], memory_mib: int,
                    core: int = 25, retired: frozenset[str] = frozenset()) -> dict:
    # R5 fix: isolate physical scope BEFORE merging claims.
    local = [e for e in evidence if (e.cluster, e.device) == (device.cluster, device.device)]
    claims = effective_claims(local, retired)
    used = len(claims); used_mib = sum(v[0] for v in claims.values())
    if used > device.slots or used_mib > device.budget_mib:
        raise ValueError('CAPACITY_INVARIANT_VIOLATION')
    if device.mode == 'WHOLE_EXCLUSIVE':
        if device.slots != 1:
            raise ValueError('WHOLE_SCOPE_CONFLICT')
        free = int(used == 0 and device.eligible)
        return {'nominal': 1, 'committed': used, 'free': free,
                'committed_mib': used_mib, 'complete': device.eligible}
    if device.mode != 'SHARED_FIXED':
        raise ValueError('UNKNOWN_MODE')
    natural(memory_mib, 'profile memory', positive=True)
    nominal = min(device.slots, device.budget_mib // memory_mib)
    free = min(device.slots - used, (device.budget_mib - used_mib) // memory_mib)
    if device.additive_core_budget is not None:
        natural(core, 'core', positive=True, maximum=100)
        used_core = sum(v[1] for v in claims.values())
        if used_core > device.additive_core_budget:
            raise ValueError('CAPACITY_INVARIANT_VIOLATION')
        nominal = min(nominal, device.additive_core_budget // core)
        free = min(free, (device.additive_core_budget - used_core) // core)
    return {'nominal': nominal, 'committed': used, 'free': free if device.eligible else 0,
            'committed_mib': used_mib, 'complete': device.eligible}


def aggregate_capacity(devices: Iterable[Device], evidence: Iterable[Evidence], memory_mib: int,
                       retired: frozenset[str] = frozenset()) -> dict:
    devices, evidence = list(devices), list(evidence)
    if len({d.mode for d in devices}) > 1:
        raise ValueError('MIXED_SUPPLY_UNITS')
    result = {'known_free_units': 0, 'complete': True, 'devices': {}, 'issues': {}}
    seen = set()
    for d in devices:
        key = (d.cluster, d.device)
        if key in seen:
            raise ValueError('DUPLICATE_PHYSICAL_DEVICE')
        seen.add(key)
        name = '/'.join(key)
        try:
            value = device_capacity(d, evidence, memory_mib, retired=retired)
            result['devices'][name] = value
            result['known_free_units'] += value['free']
            result['complete'] = result['complete'] and value['complete']
        except ValueError as exc:
            result['issues'][name] = str(exc)
            result['complete'] = False
    return result


def demo() -> dict:
    return {
        'shared_6_gib': [{ 'factor': f, 'memory_blocks': encode_shared(6144, f),
                           'quota_mib': quota_vector('SHARED_FIXED', 1, 1, 6144)['gpu.shared_memory_mib'] }
                         for f in (1, 256, 1024)],
        'two_replicas': {'per_pod_memory_blocks': encode_shared(6144, 1024),
                         'quota': quota_vector('SHARED_FIXED', 2, 1, 6144)},
        'physical_tail': reported_capacity([49140 * MIB], 256),
        'whole_quota': quota_vector('WHOLE_EXCLUSIVE', 1, 1, 0),
        'release_first_and_duplicate': [released_total(12288, 0, 12288),
                                        released_total(12288, 12288, 12288)],
    }


if __name__ == '__main__':
    print(json.dumps(demo(), ensure_ascii=False, indent=2))
