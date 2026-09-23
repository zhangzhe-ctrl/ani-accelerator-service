import unittest
from dataclasses import replace
from pathlib import Path
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'examples'))
from design_reference import *

class FactorTests(unittest.TestCase):
    def test_factor_one(self): self.assertEqual(encode_shared(6144,1),6144)
    def test_factor_256(self): self.assertEqual(encode_shared(6144,256),24)
    def test_factor_1024(self): self.assertEqual(encode_shared(6144,1024),6)
    def test_not_divisible(self):
        with self.assertRaisesRegex(ValueError,'GRANULARITY'): encode_shared(6144,10)
    def test_zero_factor(self):
        with self.assertRaises(ValueError): encode_shared(6144,0)
    def test_float_rejected(self):
        with self.assertRaises(ValueError): encode_shared(6144.0,1)
    def test_bool_rejected(self):
        with self.assertRaises(ValueError): encode_shared(6144,True)
    def test_provider_integer_range(self):
        with self.assertRaisesRegex(ValueError,'PROVIDER_INT32'): encode_shared(MAX_I32+1,1)
    def test_per_device_floor(self):
        out=reported_capacity([1500*MIB,1500*MIB],1024)
        self.assertEqual(out['blocks'],2) # not floor(3000/1024) coincidence=2
        self.assertEqual(reported_capacity([2000*MIB,2000*MIB],1024)['blocks'],2) # floor(sum)=3 is wrong
    def test_physical_tail(self):
        d=reported_capacity([49140*MIB],256)['per_device'][0]
        self.assertEqual((d['blocks'],d['reported_mib'],d['tail_mib']),(191,48896,244))
    def test_group_factors_match(self): self.assertEqual(validate_group_factors([1024,1024]),1024)
    def test_group_factor_mismatch(self):
        with self.assertRaisesRegex(ValueError,'FACTOR_MISMATCH'): validate_group_factors([1,1024])
    def test_empty_factor_unknown(self):
        with self.assertRaises(ValueError): validate_group_factors([])
    def test_runtime_limit_roundtrip(self):
        for f in (1,256,1024): self.assertEqual(encode_shared(6144,f)*f,6144)

class QuotaTests(unittest.TestCase):
    def test_shared_total_is_mib(self): self.assertEqual(quota_vector('SHARED_FIXED',2,1,6144),{'gpu.shared_memory_mib':12288})
    def test_whole_not_double_charged(self): self.assertEqual(quota_vector('WHOLE_EXCLUSIVE',2,1,0),{'gpu.physical.count':2})
    def test_multiple_devices_later(self):
        with self.assertRaisesRegex(ValueError,'SHAPE'): quota_vector('SHARED_FIXED',1,2,6144)
    def test_overflow(self):
        with self.assertRaisesRegex(ValueError,'OVERFLOW'): checked_product(MAX_I64,2)
    def test_release_duplicate(self):
        self.assertEqual(released_total(12288,0,12288),(12288,12288))
        self.assertEqual(released_total(12288,12288,12288),(12288,0))
    def test_release_out_of_order(self): self.assertEqual(released_total(12288,12288,6144),(12288,0))
    def test_release_over_original(self):
        with self.assertRaises(ValueError): released_total(12288,0,12289)
    def test_empty_charges_not_ended(self):
        with self.assertRaises(ValueError): desired_usage_revision('ACKED',[])
    def test_partial_not_ended(self): self.assertEqual(desired_usage_revision('ACKED',[(6144,6144),(6144,0)]),1)
    def test_full_ended(self): self.assertEqual(desired_usage_revision('UNKNOWN',[(12288,12288)]),2)
    def test_canceled_requires_refund(self):
        with self.assertRaises(ValueError): desired_usage_revision('CANCELED_UNSENT',[(12288,0)])
    def test_unsent_ignores_projection(self):
        self.assertTrue(can_cancel_unsent('QUEUED',0))
        self.assertFalse(can_cancel_unsent('UNKNOWN',1))
        self.assertFalse(can_cancel_unsent('DISPATCHING',0))

class ProjectionTests(unittest.TestCase):
    def setUp(self):
        self.a=UsageMessage(('tenant','ani-inference','resource','create'),1,'digest','','accepted','2026-09-23T00:00:00Z')
        self.b=replace(self.a,revision=2,end_reason='OWNER_RESOURCE_RELEASED',source_fact_ref='fully-released')
        self.repo=UsageProjection()
    def test_normal(self):
        self.assertEqual(self.repo.sync(self.a),(1,False)); self.assertEqual(self.repo.sync(self.b),(2,False))
    def test_terminal_first(self):
        self.repo.sync(self.b); self.assertEqual(self.repo.sync(self.a),(2,True))
    def test_repeated(self):
        self.repo.sync(self.b); self.assertEqual(self.repo.sync(self.b),(2,True))
    def test_same_revision_conflict(self):
        self.repo.sync(self.a)
        with self.assertRaises(ValueError): self.repo.sync(replace(self.a,source_fact_ref='different'))
    def test_plan_conflict(self):
        self.repo.sync(self.b)
        with self.assertRaises(ValueError): self.repo.sync(replace(self.a,plan_digest='other'))
    def test_new_create_cannot_revive(self):
        self.repo.sync(self.b)
        with self.assertRaises(ValueError): self.repo.sync(replace(self.a,ref=('tenant','ani-inference','resource','new-create')))
    def test_deterministic_payload(self): self.assertEqual(digest(asdict(self.a)),digest(asdict(self.a)))

class CapacityTests(unittest.TestCase):
    def setUp(self):
        self.d=Device('c','good','SHARED_FIXED',24576,4)
        self.e=Evidence('one','c','good','p','inference','scheduler',6,'BLOCKS',1024,'ACTIVE')
    def test_sources_normalize_once(self):
        ev=[self.e,replace(self.e,fact_id='two',source='plugin',value=6144,unit='MIB'),
            replace(self.e,fact_id='three',source='runtime',value=6144*MIB,unit='BYTES')]
        out=device_capacity(self.d,ev,6144)
        self.assertEqual((out['committed'],out['committed_mib'],out['free']),(1,6144,3))
    def test_other_device_conflict_isolated(self):
        bad=replace(self.e,device='bad')
        ev=[bad,replace(bad,fact_id='two',source='plugin',value=12)]
        self.assertEqual(device_capacity(self.d,ev,6144)['free'],4)
        out=aggregate_capacity([self.d,replace(self.d,device='bad')],ev,6144)
        self.assertEqual(out['known_free_units'],4); self.assertFalse(out['complete'])
        self.assertIn('c/bad',out['issues'])
    def test_unlinked_still_counts(self): self.assertEqual(device_capacity(self.d,[self.e],6144)['free'],3)
    def test_unknown_still_counts(self): self.assertEqual(device_capacity(self.d,[replace(self.e,state='UNKNOWN')],6144)['free'],3)
    def test_raw_bytes_not_mib(self): self.assertEqual(replace(self.e,value=6144*MIB,unit='BYTES').normalized_mib(),6144)
    def test_double_factor_is_conflict(self):
        with self.assertRaises(ValueError):
            device_capacity(self.d,[self.e,replace(self.e,fact_id='bad',source='runtime',value=6144)],6144)
    def test_retirement_required_for_old_selection(self):
        ev=[self.e,replace(self.e,fact_id='released',source='plugin',state='RELEASE_CONFIRMED')]
        self.assertEqual(device_capacity(self.d,ev,6144)['free'],3)
        self.assertEqual(device_capacity(self.d,ev,6144,retired=frozenset({'one'}))['free'],4)
    def test_physical_safe_three(self): self.assertEqual(device_capacity(replace(self.d,budget_mib=23552,slots=3),[],6144)['free'],3)
    def test_slot_full(self):
        ev=[replace(self.e,pod_uid=f'p{i}',fact_id=f'f{i}') for i in range(4)]
        self.assertEqual(device_capacity(self.d,ev,6144)['free'],0)
    def test_whole_separate(self):
        whole=Device('c','whole','WHOLE_EXCLUSIVE',49152,1)
        self.assertEqual(device_capacity(whole,[self.e],0)['free'],1)
    def test_whole_occupied(self):
        whole=Device('c','good','WHOLE_EXCLUSIVE',24576,1)
        claim=replace(self.e,value=24,core=100)
        self.assertEqual(device_capacity(whole,[claim],0)['free'],0)
    def test_ineligible_is_unknown(self):
        x=device_capacity(replace(self.d,eligible=False),[self.e],6144)
        self.assertEqual(x['free'],0); self.assertFalse(x['complete']); self.assertEqual(x['committed'],1)
    def test_over_budget_not_clamped(self):
        with self.assertRaises(ValueError): device_capacity(self.d,[replace(self.e,value=30)],6144)
    def test_whole_and_shared_not_summed(self):
        with self.assertRaisesRegex(ValueError,'MIXED_SUPPLY'): aggregate_capacity([self.d,Device('c','whole','WHOLE_EXCLUSIVE',49152,1)],[],6144)
    def test_duplicate_physical_not_summed(self):
        with self.assertRaises(ValueError): aggregate_capacity([self.d,self.d],[],6144)

if __name__=='__main__': unittest.main()
