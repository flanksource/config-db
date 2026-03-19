#!/usr/bin/env python3
"""
pprof-hotspots.py

Parse `go tool pprof -top ...` text output and extract the biggest changed
functions (by absolute cumulative delta).

Usage:
  pprof-hotspots.py \
    --input pprof-cpu-diff-cum.txt \
    --metric cpu \
    --prefix github.com/flanksource/config-db/ \
    --top 8 \
    --functions-out cpu-functions.txt \
    --markdown-out cpu-hotspots.md
"""

from __future__ import annotations

import argparse
import re
from dataclasses import dataclass
from pathlib import Path


@dataclass
class Row:
    fn: str
    flat_raw: str
    cum_raw: str
    flat: float
    cum: float


def parse_cpu(value: str) -> float:
    sign = -1.0 if value.startswith("-") else 1.0
    value = value.lstrip("+-")
    m = re.match(r"^(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m)?$", value)
    if not m:
        raise ValueError(value)
    num = float(m.group(1))
    unit = m.group(2) or ""
    multipliers = {
        "": 1.0,
        "ns": 1e-9,
        "us": 1e-6,
        "µs": 1e-6,
        "ms": 1e-3,
        "s": 1.0,
        "m": 60.0,
    }
    return sign * num * multipliers[unit]


def parse_bytes(value: str) -> float:
    sign = -1.0 if value.startswith("-") else 1.0
    value = value.lstrip("+-")
    m = re.match(r"^(\d+(?:\.\d+)?)(B|kB|KB|MB|GB|TB|KiB|MiB|GiB|TiB)?$", value)
    if not m:
        raise ValueError(value)
    num = float(m.group(1))
    unit = m.group(2) or "B"
    multipliers = {
        "B": 1.0,
        "kB": 1e3,
        "KB": 1e3,
        "MB": 1e6,
        "GB": 1e9,
        "TB": 1e12,
        "KiB": 1024.0,
        "MiB": 1024.0**2,
        "GiB": 1024.0**3,
        "TiB": 1024.0**4,
    }
    return sign * num * multipliers[unit]


def parse_rows(path: Path, metric: str, prefix: str) -> list[Row]:
    parser = parse_cpu if metric == "cpu" else parse_bytes
    rows: list[Row] = []

    for line in path.read_text().splitlines():
        parts = line.strip().split()
        if len(parts) < 6:
            continue

        flat_raw, cum_raw = parts[0], parts[3]
        fn = " ".join(parts[5:])

        if not fn.startswith(prefix):
            continue

        try:
            flat = parser(flat_raw)
            cum = parser(cum_raw)
        except Exception:
            continue

        if flat == 0 and cum == 0:
            continue

        rows.append(Row(fn=fn, flat_raw=flat_raw, cum_raw=cum_raw, flat=flat, cum=cum))

    # keep highest absolute cumulative change first
    rows.sort(key=lambda r: abs(r.cum), reverse=True)
    return rows


def render_markdown(rows: list[Row], top: int) -> str:
    if not rows:
        return "_No project hotspots found in profile diff._\n"

    lines = [
        "| Function | Cum Δ | Flat Δ |",
        "|----------|-------|--------|",
    ]

    for row in rows[:top]:
        lines.append(f"| `{row.fn}` | {row.cum_raw} | {row.flat_raw} |")

    return "\n".join(lines) + "\n"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--input", required=True)
    ap.add_argument("--metric", choices=["cpu", "mem"], required=True)
    ap.add_argument("--prefix", default="github.com/flanksource/config-db/")
    ap.add_argument("--top", type=int, default=8)
    ap.add_argument("--functions-out", required=True)
    ap.add_argument("--markdown-out", required=True)
    args = ap.parse_args()

    rows = parse_rows(Path(args.input), args.metric, args.prefix)

    with open(args.functions_out, "w") as f:
        for row in rows[: args.top]:
            f.write(row.fn + "\n")

    with open(args.markdown_out, "w") as f:
        f.write(render_markdown(rows, args.top))


if __name__ == "__main__":
    main()
