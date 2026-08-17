#!/usr/bin/env python3
"""Audit web/ stylesheets for drift from the token system.

Complements web/src/styles/styles.guard.test.ts, which already enforces raw
hex colours, border-radius tokens and box-shadow tokens. This covers the checks
that guard does not:

  1. raw font-size / spacing / radius values that bypass the token scale
  2. --control-height defeated by vertical padding (the height token goes inert
     because padding plus the line box already exceeds it)
  3. letter-spacing that does not match the size tier copied from Geist
  4. box-shadow off-token (kept as a backstop; duplicated in the guard test)

Usage:
    python3 scripts/audit-css.py          # report, exit 1 if findings
    python3 scripts/audit-css.py --list   # report only, always exit 0

Exit code is the gate: 0 clean, 1 findings.
"""
import argparse
import collections
import os
import re
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
STYLES = os.path.join(REPO, "web", "src", "styles")
FILES = ["base.css", "primitives.css", "features.css"]

SPACE = {4: "--space-1", 8: "--space-2", 12: "--space-3", 16: "--space-4", 20: "--space-5",
         24: "--space-6", 32: "--space-8", 40: "--space-10", 48: "--space-12",
         64: "--space-16", 96: "--space-24"}
RADIUS = {4: "--radius-sm", 6: "--radius-md", 8: "--radius-lg", 12: "--radius-xl",
          9999: "--radius-full"}
TEXT = {12: "--text-xs", 14: "--text-sm", 16: "--text-base", 18: "--text-lg", 20: "--text-xl",
        24: "--text-2xl", 32: "--text-3xl", 40: "--text-4xl", 52: "--text-5xl",
        64: "--text-6xl", 84: "--text-7xl"}
TEXT_PX = {v: k for k, v in TEXT.items()}

# Sizes that are deliberately off-scale. Each needs a reason, so that silencing
# a finding stays a decision rather than a habit.
ALLOWED = {
    (".log-panel pre", "min-height"): "log viewport, an arbitrary scroll height with no token equivalent",
    (".role-badge", "min-width"): "sized to the longest role label so the pills form a column",
    ("body", "min-width"): "minimum supported viewport width, not a spacing value",
}

# Geist tightens tracking in three tiers as type grows.
def expected_tracking(px):
    if px >= 40:
        return "--tracking-tightest"
    if px >= 24:
        return "--tracking-tighter"
    return "--tracking-tight"


def rules(path):
    with open(path, encoding="utf-8") as fh:
        css = re.sub(r"/\*.*?\*/", "", fh.read(), flags=re.S)
    for match in re.finditer(r"([^{}]+)\{([^{}]*)\}", css):
        selector = " ".join(match.group(1).split())
        if not selector or selector.startswith("@"):
            continue
        declarations = {}
        for decl in match.group(2).split(";"):
            if ":" in decl:
                key, value = decl.split(":", 1)
                declarations[key.strip()] = value.strip()
        yield selector, declarations


def audit():
    findings = collections.defaultdict(list)
    for name in FILES:
        path = os.path.join(STYLES, name)
        if not os.path.exists(path):
            continue
        for selector, decl in rules(path):
            _check_raw_values(name, selector, decl, findings)
            _check_control_height(name, selector, decl, findings)
            _check_tracking_tier(name, selector, decl, findings)
            _check_shadow(name, selector, decl, findings)
    return findings


def _check_raw_values(name, selector, decl, findings):
    props = ("font-size", "border-radius", "gap", "padding", "margin",
             "min-height", "min-width",
             "padding-top", "padding-bottom", "padding-left", "padding-right")
    for prop in props:
        value = decl.get(prop)
        if not value or "var(" in value:
            continue
        if ALLOWED.get((selector, prop)):
            continue
        for number, unit in re.findall(r"(-?\d*\.?\d+)(rem|px)", value):
            px = float(number) * (16 if unit == "rem" else 1)
            if px in (0, 1, 2):  # hairlines and resets are not scale values
                continue
            table = TEXT if prop == "font-size" else RADIUS if prop == "border-radius" else SPACE
            token = table.get(int(px))
            hint = f"use var({token})" if token else \
                   f"off-scale, nearest is {min(table, key=lambda t: abs(t - px))}px"
            findings["raw values bypassing the token scale"].append(
                f"{name}: {selector}\n      {prop}: {value}  ->  {px:g}px, {hint}")


def _check_control_height(name, selector, decl, findings):
    if "--control-height" not in decl.get("min-height", ""):
        return
    vertical = None
    padding = decl.get("padding", "").split()
    if padding and padding[0] not in ("0", "0px"):
        vertical = padding[0]
    for side in ("padding-top", "padding-bottom"):
        if decl.get(side, "0") not in ("0", "0px", ""):
            vertical = decl[side]
    if vertical:
        findings["--control-height defeated by vertical padding"].append(
            f"{name}: {selector}\n      min-height uses --control-height but vertical "
            f"padding is {vertical}; the token cannot govern the height")


def _check_tracking_tier(name, selector, decl, findings):
    size, spacing = decl.get("font-size", ""), decl.get("letter-spacing", "")
    if not (size and spacing and "--text-" in size and "--tracking-tight" in spacing):
        return
    token = re.search(r"--text-[\w]+", size).group(0)
    px = TEXT_PX.get(token)
    if not px:
        return
    have = re.search(r"--tracking-[\w]+", spacing).group(0)
    want = expected_tracking(px)
    if have != want:
        findings["tracking tier does not match size"].append(
            f"{name}: {selector}\n      {token} is {px}px, which wants {want}, but has {have}")


def _check_shadow(name, selector, decl, findings):
    shadow = decl.get("box-shadow", "")
    if shadow and "var(" not in shadow and shadow != "none":
        findings["box-shadow not using a --shadow-* token"].append(
            f"{name}: {selector}\n      {shadow}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--list", action="store_true",
                        help="report findings but always exit 0")
    args = parser.parse_args()

    findings = audit()
    total = sum(len(v) for v in findings.values())

    for heading, entries in findings.items():
        print(f"\n{'=' * 70}\n{heading.upper()}  ({len(entries)})\n{'=' * 70}")
        for entry in entries:
            print("  " + entry)

    if total == 0:
        print("CSS audit: no findings.")
        return 0
    print(f"\n\nCSS audit: {total} finding(s).")
    return 0 if args.list else 1


if __name__ == "__main__":
    sys.exit(main())
