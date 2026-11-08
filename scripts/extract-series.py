#!/usr/bin/env python3
"""Copy recorded per-second series out of an evaluation snapshot into detector test fixtures.

usage: scripts/extract-series.py reports/snapshots/dev-work_mem_spill.json internal/detect/testdata/work_mem_spill.json
"""
import json, sys

snap = json.load(open(sys.argv[1]))
names = {int(k): v for k, v in snap["names"].items()}
out = {"faultAt": snap["timeline"]["faultAt"], "fixAt": snap["timeline"]["fixAt"], "queries": {}}
for q in snap["snapshot"]["queries"]:
    name = names.get(q["id"])
    if not name:
        continue
    out["queries"][name] = [
        {"at": p["at"], "calls": p["calls"], "execMs": round(p["meanMs"] * p["calls"], 4),
         "tempBlksWritten": round(p["tempPerCall"] * p["calls"])}
        for p in q["series"]
    ]
json.dump(out, open(sys.argv[2], "w"), indent=0)
