"""Print bounded source contexts for manual review; performs no network requests."""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / 'artifacts/firecrawl-comparison-20260914'
pages = json.loads((ROOT / 'scripts/eval/page-reader/dataset.json').read_text(encoding='utf-8'))
for p in pages:
    print('\nPAGE', p['id'])
    for method in ['http', 'browser', 'firecrawl']:
        file = OUT / ('firecrawl/first-' + p['id'] + '.json' if method == 'firecrawl' else 'baseline/' + p['id'] + '-' + method + '.json')
        row = json.loads(file.read_text(encoding='utf-8'))
        body = row.get('data', {}).get('markdown', '') if method == 'firecrawl' else row.get('page', {}).get('text', '')
        text = body.replace('\\_', '_')
        print(method, 'chars', len(body), 'error', row.get('error'), 'status', row.get('data', {}).get('metadata', {}).get('statusCode'))
        if p.get('exclude') or not p['positive'] or p['id'] == 'asus':
            print('HEAD', text[:1200])
        for point in p['points']:
            found = []
            for term in point[1:]:
                pos = text.lower().find(term.lower())
                found.append((term, pos, text[max(0,pos-100):pos+len(term)+150] if pos >= 0 else ''))
            if found:
                print(json.dumps(found, ensure_ascii=False))
