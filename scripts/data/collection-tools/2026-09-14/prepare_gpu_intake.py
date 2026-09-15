"""Exact MSI board intake, reusing the saved-source reviewed publication packet."""
import argparse
from pathlib import Path
from prepare_ssd_intake import prepare

CHOICES=[("gpu-rtx5060ti8g-msi","msi-5060ti-8g-ventus3x-oc","MSI","GeForce RTX 5060 Ti 8G VENTUS 3X OC",
          "cc759574e5866235","微星万图师 GeForce RTX 5060 Ti 8G VENTUS 3X OC")]


def check_spec(key,text):
    required=["Marketing Name\nGeForce RTX™ 5060 Ti 8G VENTUS 3X OC", "Model Name\nG506T-8V3C",
              "Power consumption\n180 W", "Power Connectors\n8-pin x 1", "Card Dimension(mm)\n304 x 121 x 44 mm"]
    if any(value not in text for value in required):
        raise ValueError("MSI board identity/power/connector/dimension block changed")
    return required, {"tdp_w":(180,required[2]),"power_connectors":(["pcie_8pin"],required[3]),"length_mm":(304,required[4])}


if __name__=="__main__":
    parser=argparse.ArgumentParser()
    parser.add_argument("--out",required=True,type=Path)
    parser.add_argument("--pages",required=True,type=Path)
    args=parser.parse_args()
    args.out.mkdir(parents=True,exist_ok=False)
    prepare(args.out,args.pages,choices=CHOICES,check=check_spec,category="gpu")
