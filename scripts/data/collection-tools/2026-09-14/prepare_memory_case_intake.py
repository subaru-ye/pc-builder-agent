"""Reviewed two-category packet: official specs plus separately captured details."""
import argparse
import json
from decimal import Decimal
from pathlib import Path
from urllib.parse import urlparse
import yaml

from audit_candidates import ROOT, digest, read_rows
from prepare_amd_intake import jsonl
from prepare_ssd_intake import prepare

CHOICES = [
    ('case-lianli-lancool-207', 'lianli-lancool207', 'Lian Li', 'LANCOOL 207 (颜色未明确)',
     'f86260e8a97e7a86', 'LIANLI联力 鬼斧LANCOOL 207'),
    ('mem-kingbank-xingren-48-6000', 'kingbank-xingren-black', 'KINGBANK',
     '星刃 KFRB DDR5-6000 CL28 48GB (2x24GB)', '22dcdb8990630df3', '星刃48G(24G*2)6000RGB套条C28'),
]
REVIEWED_PRICES = {'case-lianli-lancool-207': '584.29', 'mem-kingbank-xingren-48-6000': '3499'}


def check_spec(key, text):
    if key == 'lianli-lancool207':
        required = ['MODEL NAME\nLANCOOL 207',
                    'MOTHERBOARD SUPPORT\nStandard ATX ( Width Max 244mm ) /Micro-ATX/Mini-ITX',
                    'PSU SUPPORT\nATX (Under 160mm)', 'RADIATOR SUPPORT\nTop: 360/280/240mm',
                    'GPU LENGTH CLEARANCE\n375mm (Max.)', 'CPU HEIGHT CLEARANCE\n180mm (Max.)']
        fields = {'supported_form_factors': (['atx', 'matx', 'itx'], required[1]),
                  'radiator_sizes_mm': ([240, 280, 360], required[3]),
                  'gpu_length_max_mm': (375, required[4]), 'cooler_height_max_mm': (180, required[5])}
    elif key == 'kingbank-xingren-black':
        required = ['KINGBANK 金百达 星刃黑系列 KFRB DDR5 RGB 内存',
                    '48GB(24GBx2)\n6000MT/s\nCL28\nHynix-M\nIntel/AMD\nK5.01.FLM5EM9502']
        fields = {'generation': ('DDR5', required[0]), 'speed_mts': (6000, required[1])}
    else:
        raise ValueError('unreviewed official product')
    if any(block not in text for block in required):
        raise ValueError('reviewed official identity/spec block changed: ' + key)
    return required, fields


def reviewed_detail(offer, index, folder):
    if not index['ok'] or index['goodsId'] != offer['goodsId'] or str(index['source']) != offer['platform_src']:
        raise ValueError('detail request does not match original offer')
    source = folder / index['file']
    if digest(source) != index['sha256']:
        raise ValueError('saved detail file changed')
    payload = yaml.safe_load(source.read_text(encoding='utf-8'))
    detail = payload['商品详情']
    normalize = lambda value: ' '.join(value.split())
    if normalize(payload['商品标题']) != normalize(offer['title']) or detail['shopName'] != offer['shop']:
        raise ValueError('detail title/seller differs from reviewed offer')
    if str(detail['platformId']) != offer['platform_src']:
        raise ValueError('detail platform mismatch')
    price = Decimal(str(detail['actualPrice']))
    if price != Decimal(REVIEWED_PRICES[offer['model_id']]):
        raise ValueError('detail price drifted from manual review')
    link = urlparse(payload['购买链接'])
    if link.scheme != 'https' or link.username or link.password or link.netloc not in {'m.tb.cn', 'u.jd.com'}:
        raise ValueError('unsupported detail purchase link')
    # This is a new observation, not a rewrite of the old 09-13 quote.
    return {**offer, 'price_cny': str(price), 'buy_url': payload['购买链接'],
            'detail_observed_date': index['ts'][:10], 'detail_captured_at': index['ts'],
            'detail_source_file': source.resolve().relative_to(ROOT).as_posix(),
            'detail_source_sha256': index['sha256'], 'detail_payload': payload,
            'original_offer': offer, 'original_offer_file_sha256': digest(ROOT/'scripts/data/catalog-candidates/2026-09-13/offers.jsonl')}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--pages', required=True, type=Path)
    parser.add_argument('--details', required=True, type=Path)
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    offers = {o['row_key']: o for o in read_rows('offers.jsonl')}
    index_rows = [json.loads(line) for line in (args.details/'raw_index.jsonl').read_text(encoding='utf-8').splitlines()]
    index = {row['detail_id']: row for row in index_rows}
    if len(index) != len(index_rows): raise ValueError('ambiguous repeated detail response')
    selected = [reviewed_detail(offers[row_key], index[sku], args.details) for sku, _, _, _, row_key, _ in CHOICES]
    args.out.mkdir(parents=True, exist_ok=False)
    source = args.out/'reviewed-detail-offers.jsonl'
    jsonl(source, selected)
    prepare(args.out, args.pages, choices=CHOICES, check=check_spec, category=None,
            offer_source=source, offer_sha256=digest(source))


if __name__ == '__main__': main()
