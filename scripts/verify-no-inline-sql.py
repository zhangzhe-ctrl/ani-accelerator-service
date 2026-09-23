#!/usr/bin/env python3
"""Reject embedded SQL and arbitrary query execution outside regenerated sqlc.

The generated directory is exempt only after verify-sqlc has regenerated it
without a diff. Tests keep SQL in testdata files and are checked separately.
"""
import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parents[1]
errors = []
literal = re.compile(r'`[^`]*`|"(?:\\.|[^"\\])*"', re.S)
sql = re.compile(r'\b(?:SELECT\s+.+?\s+FROM|INSERT\s+INTO|UPDATE\s+\w+[.\w]*\s+SET|DELETE\s+FROM|CREATE\s+(?:TEMP\s+)?TABLE|ALTER\s+TABLE|DROP\s+TABLE|SET\s+(?:ROLE|row_security))\b', re.I | re.S)
for base in ('internal', 'cmd', 'tests'):
    for path in (root / base).rglob('*.go'):
        relative = path.relative_to(root)
        if relative.parts[:3] == ('internal', 'data', 'accdb'):
            continue
        source = path.read_text()
        for match in literal.finditer(source):
            if sql.search(match.group()):
                errors.append(f'{relative}:{source.count(chr(10), 0, match.start()) + 1}: SQL belongs in query, migration or test SQL source')
        if not path.name.endswith('_test.go') and re.search(r'\.(?:Exec|QueryRow|Query)\([^)]*,', source):
            errors.append(f'{relative}: arbitrary SQL execution is forbidden outside generated sqlc')
if errors:
    print('\n'.join(errors), file=sys.stderr)
    sys.exit(1)
print('PASS: no inline SQL or arbitrary production SQL execution')
