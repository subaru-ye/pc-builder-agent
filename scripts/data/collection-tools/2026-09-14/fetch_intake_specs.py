"""Bounded manual official-page capture; no search API, prices or publishing."""
import argparse
import gzip
import io
import json
import subprocess
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from audit_candidates import ROOT, digest, write_json
from pcdata.automation import _VisiblePolicyTextParser
from pcdata.amd import parse_amd_cpu_html

PAGES = {
    "msi-b650i-edge-wifi":"https://www.msi.com/Motherboard/MPG-B650I-EDGE-WIFI/Specification",
    "lianli-lancool207":"https://lian-li.com/product/lancool-207/",
    "kingbank-xingren-black":"https://www.kingbank.com/zh-cn/MemoryProduct_zh-cn/2468.html",
    "adata-d300g":"https://xpg.adata.com.cn/cn/xpg/dram-modules-lancer-blade-rgb-ddr5",
    "fractal-pop-mini-silent-sheet":"https://www.fractal-design.com/app/uploads/2022/06/Pop-Mini-Silent_Product-Sheet_EN.pdf",
    "sparkle-a750-orc-sheet":"https://www.sparkle.com.tw/files/20240202122016342.pdf",
    "msi-5060ti-8g-ventus3x-oc":"https://us.msi.com/Graphics-Card/GeForce-RTX-5060-Ti-8G-VENTUS-3X-OC/Specification",
    "asrock-7600xt-sl":"https://www.asrock.com/graphics-Card/AMD/Radeon%20RX%207600%20XT%20Steel%20Legend%2016GB%20OC/",
    "sparkle-a750-orc":"https://www.sparkle.com.tw/tw/products/view/F2CE94cd39B7",
    "zhitai-ti600":"https://www.ymtc.com/en/products/39.html?cat=41",
    "wd-sn7100":"https://www.sandisk.com/products/ssd/internal-ssd/wd-black-sn7100-nvme-internal-ssd?sku=WDS500G4X0E-00CJA0",
    "amd-5700x3d":"https://www.amd.com/en/support/downloads/drivers.html/processors/ryzen/ryzen-5000-series/amd-ryzen-7-5700x3d.html",
    "kingston-nv3":"https://www.kingston.com/en/company/press/article/74016",
    "klevv-fitv":"https://www.klevv.com/ken/products_details/memory/Klevv_FITV",
}
AMD_PRODUCTS = {
    "amd-5700x3d":("5000","7","5700X3D"),
    "amd-4500":("4000","5","4500"),
    "amd-5600gt":("5000","5","5600GT"),
    "amd-5800x3d":("5000","7","5800X3D"),
    "amd-8400f":("8000","5","8400F"),
    "amd-8700f":("8000","7","8700F"),
    "amd-8700g":("8000","7","8700G"),
    "amd-4600g":("4000","5","4600G"),
    "amd-5600g":("5000","5","5600G"),
    "amd-5700g":("5000","7","5700G"),
    "amd-7900x":("7000","9","7900X"),
    "amd-9950x3d":("9000","9","9950X3D"),
}
for key,(series,tier,model) in AMD_PRODUCTS.items():
    PAGES[key]=f"https://www.amd.com/en/support/downloads/drivers.html/processors/ryzen/ryzen-{series}-series/amd-ryzen-{tier}-{model.lower()}.html"


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def decode_page(body):
    # Some origins send gzip even without Accept-Encoding. Preserve raw bytes;
    # cap decompressed content as well as the transport body.
    if body.startswith(b"\x1f\x8b"):
        with gzip.GzipFile(fileobj=io.BytesIO(body)) as stream:
            body=stream.read(4*1024*1024+1)
    if len(body)>4*1024*1024:
        raise ValueError("decoded response exceeds 4 MiB")
    return body.decode("utf-8")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--out",required=True,type=Path)
    parser.add_argument("--pages",nargs="+",choices=tuple(PAGES),required=True)
    args = parser.parse_args()
    args.out.mkdir(parents=True,exist_ok=False)
    results = []
    opener = urllib.request.build_opener(NoRedirect())
    for name in dict.fromkeys(args.pages):
        url=PAGES[name]
        row = {"id":name,"url":url,"captured_at":datetime.now(timezone.utc).isoformat(),"status":"unread"}
        start=time.monotonic()
        try:
            request=urllib.request.Request(url,headers={"User-Agent":"PCBuilder-Manual-Research/1.0","Accept":"text/html"})
            with opener.open(request,timeout=20) as response:
                body=response.read(4*1024*1024+1)
                if len(body)>4*1024*1024:
                    raise ValueError("response exceeds 4 MiB")
                row["http_status"]=response.status
                row["content_type"]=response.headers.get("Content-Type")
                row["content_encoding"]=response.headers.get("Content-Encoding")
            is_pdf="application/pdf" in (row["content_type"] or "") or body.startswith(b"%PDF-")
            path=args.out/(name+(".pdf" if is_pdf else ".html"))
            path.write_bytes(body)
            row["raw_sha256"]=digest(path)
            if is_pdf:
                subprocess.run(["pdftotext","-layout",str(path),str(args.out/(name+".txt"))],check=True,timeout=30)
                row["status"]="captured_pdf_needs_review"
                row["text_sha256"]=digest(args.out/(name+".txt"))
                row["duration_ms"]=round((time.monotonic()-start)*1000)
                results.append(row)
                write_json(args.out/"manifest.json",{"requests":len(results),"retries":0,"pages":results})
                print(name,row["status"],flush=True)
                continue
            reader=_VisiblePolicyTextParser()
            reader.feed(decode_page(body))
            (args.out/(name+".txt")).write_bytes(("\n".join(reader.parts)+"\n").encode())
            row["status"]="captured_needs_review"
            if name in AMD_PRODUCTS:
                _,tier,model=AMD_PRODUCTS[name]
                fields,quotes=parse_amd_cpu_html(body,f"AMD Ryzen {tier} {model}")
                row.update(fields=fields,quotes=quotes,status="parsed_needs_publication_review")
        except Exception as error:
            row["error"]=str(error)
        row["duration_ms"]=round((time.monotonic()-start)*1000)
        results.append(row)
        write_json(args.out/"manifest.json",{"requests":len(results),"retries":0,"pages":results})
        print(name,row["status"],row.get("error",""),flush=True)
        if row.get("error") and any(code in row["error"] for code in ("403","406","429")):
            break
    # Failure is preserved and explicit; no alternative service or retry.
    if any(r.get("error") for r in results):
        raise SystemExit(1)


if __name__=="__main__":
    main()
