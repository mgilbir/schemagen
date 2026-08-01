#!/usr/bin/env python3
"""Check that every fuzz seed is a legal JSON Schema document.

Each seed is validated as an *instance* against the meta-schema for its own
dialect, using Bowtie to drive several independent implementations in
containers. Running more than one is the point: a single library's opinion is
not evidence, and where they disagree the answer is "unknown", not "valid".

Deliberately out of the Go build -- no third-party validator reaches go.mod.
Requires docker and uv; nothing else.
"""

import argparse
import json
import os
import subprocess
import sys
from collections import defaultdict

BOWTIE = ["uvx", "--from", "bowtie-json-schema", "bowtie"]

# Dialect chosen when a document does not say. An absent $schema means the
# newest draft, which is exactly the trap that made four seeds look illegal:
# array-form "items" is draft-07 and a schema-valued "type" is draft-03, so
# read as 2020-12 neither is legal. Reporting them is the intent.
DEFAULT_DIALECT = "https://json-schema.org/draft/2020-12/schema"


def load_metaschemas(meta_dir):
    """Map dialect URI -> path, keyed by the $id each document declares."""
    out = {}
    for name in sorted(os.listdir(meta_dir)):
        if not name.endswith(".json"):
            continue
        path = os.path.join(meta_dir, name)
        try:
            with open(path) as fh:
                doc = json.load(fh)
        except (OSError, ValueError):
            continue
        if not isinstance(doc, dict):
            continue
        ident = doc.get("$id") or doc.get("id")
        if not ident:
            continue
        out[ident] = path
        # Drafts differ on the trailing "#"; accept a lookup either way.
        out[ident.rstrip("#")] = path
    return out


def dialect_of(doc):
    if not isinstance(doc, dict):
        return DEFAULT_DIALECT, False
    declared = doc.get("$schema")
    if isinstance(declared, str) and declared:
        return declared, True
    return DEFAULT_DIALECT, False


# Bowtie decodes reports with Python's json module, which recurses per nesting
# level and dies with RecursionError well before our deepest seeds (2001
# levels). That kills the whole invocation, so every file batched alongside a
# deep one silently loses its verdict -- which looked like 60 mysteriously
# unjudgeable seeds until the batch was retried file by file. Deep seeds are
# excluded up front and reported as unchecked rather than quietly dropped.
#
# 100 is chosen from the corpus, which is sharply bimodal: the five generated
# deep/ probes nest 201-2001 levels and every hand-written seed nests at most 7.
# Anything between separates them, so the threshold needs no tuning as seeds are
# added -- only a genuinely deep new seed would trip it, which is the intent.
MAX_HARNESS_DEPTH = 100


def nesting_depth(raw):
    depth = best = 0
    in_str = esc = False
    for ch in raw:
        if esc:
            esc = False
        elif in_str:
            if ch == "\\":
                esc = True
            elif ch == '"':
                in_str = False
        elif ch == '"':
            in_str = True
        elif ch in "{[":
            depth += 1
            best = max(best, depth)
        elif ch in "}]":
            depth -= 1
    return best


def _invoke(metaschema, files, impls):
    cmd = list(BOWTIE) + ["validate"]
    for i in impls:
        cmd += ["-i", i]
    cmd += [metaschema] + files
    proc = subprocess.run(cmd, capture_output=True, text=True)
    verdicts = defaultdict(dict)
    for line in proc.stdout.splitlines():
        try:
            entry = json.loads(line)
        except ValueError:
            continue
        impl = entry.get("implementation")
        results = entry.get("results")
        if impl is None or not isinstance(results, list):
            continue
        for idx, res in enumerate(results):
            if isinstance(res, dict) and "valid" in res:
                verdicts[idx][impl] = bool(res["valid"])
            else:
                verdicts[idx][impl] = None  # errored / skipped
    return verdicts, proc


def run_bowtie(metaschema, files, impls):
    """Validate files as instances of metaschema. Returns index -> {impl: valid}.

    One unparseable file takes the whole invocation down with it, so an
    incomplete batch is retried file by file: the failure is then attributed to
    the file that actually caused it instead of to everything beside it.
    """
    verdicts, proc = _invoke(metaschema, files, impls)
    complete = all(
        len(verdicts.get(i, {})) == len(impls) and
        all(v is not None for v in verdicts.get(i, {}).values())
        for i in range(len(files))
    )
    if complete or len(files) == 1:
        if not verdicts and proc.returncode != 0:
            sys.stderr.write(proc.stderr[-400:] + "\n")
        return verdicts
    sys.stderr.write(f"   batch incomplete ({len(files)} files); retrying individually\n")
    out = defaultdict(dict)
    for idx, path in enumerate(files):
        single, _ = _invoke(metaschema, [path], impls)
        out[idx] = single.get(0, {})
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--seeds", default="testdata/schemas/adversarial")
    ap.add_argument("--metaschemas", default="testdata/external/metaschemas")
    ap.add_argument("--impl", action="append", default=None)
    ap.add_argument("--batch", type=int, default=60)
    args = ap.parse_args()
    impls = args.impl or ["python-jsonschema", "js-ajv"]

    metas = load_metaschemas(args.metaschemas)
    if not metas:
        sys.exit(f"no meta-schemas under {args.metaschemas}; run 'make download-metaschemas'")

    by_dialect = defaultdict(list)
    unparsable, too_deep = [], []
    for root, _, names in os.walk(args.seeds):
        for name in sorted(names):
            if not name.endswith(".json"):
                continue
            path = os.path.join(root, name)
            try:
                with open(path) as fh:
                    raw = fh.read()
                doc = json.loads(raw)
            except ValueError as exc:
                unparsable.append((path, str(exc)))
                continue
            depth = nesting_depth(raw)
            if depth > MAX_HARNESS_DEPTH:
                too_deep.append((path, depth))
                continue
            uri, explicit = dialect_of(doc)
            by_dialect[(uri, explicit)].append(path)

    invalid, unknown, disagreed, valid_count, unsupported = [], [], [], 0, []

    for (uri, explicit), files in sorted(by_dialect.items()):
        meta = metas.get(uri) or metas.get(uri.rstrip("#"))
        label = uri + ("" if explicit else "  (inferred - no $schema)")
        if not meta:
            unsupported.append((uri, len(files)))
            continue
        print(f"-- {label}: {len(files)} seeds", file=sys.stderr)
        for start in range(0, len(files), args.batch):
            chunk = files[start:start + args.batch]
            verdicts = run_bowtie(meta, chunk, impls)
            for idx, path in enumerate(chunk):
                per = verdicts.get(idx, {})
                vals = [v for v in per.values() if v is not None]
                if not vals:
                    unknown.append((path, uri, "no implementation returned a verdict"))
                elif len(set(vals)) > 1:
                    disagreed.append((path, uri, per))
                elif vals[0]:
                    valid_count += 1
                else:
                    invalid.append((path, uri, explicit))

    def rel(p):
        return os.path.relpath(p, args.seeds)

    if unparsable:
        print("\n== NOT JSON ==")
        for p, e in unparsable:
            print(f"  {rel(p)}: {e}")
    if disagreed:
        print("\n== IMPLEMENTATIONS DISAGREE (treat as unknown) ==")
        for p, uri, per in disagreed:
            print(f"  {rel(p)}  [{uri}]  {per}")
    if unknown:
        print("\n== NO VERDICT ==")
        for p, uri, why in unknown:
            print(f"  {rel(p)}  [{uri}]  {why}")
    if invalid:
        print("\n== NOT A VALID SCHEMA IN ITS DIALECT ==")
        for p, uri, explicit in sorted(invalid):
            note = "" if explicit else "   <- no $schema; judged as 2020-12"
            print(f"  {rel(p)}  [{uri}]{note}")
    if unsupported:
        print("\n== DIALECT HAS NO LOCAL META-SCHEMA ==")
        for uri, n in unsupported:
            print(f"  {uri}: {n} seeds unchecked")
    if too_deep:
        print("\n== TOO DEEPLY NESTED FOR THE HARNESS (unchecked) ==")
        for p, d in sorted(too_deep):
            print(f"  {rel(p)}  ({d} levels; harness limit {MAX_HARNESS_DEPTH})")

    total = (valid_count + len(invalid) + len(unknown) + len(disagreed)
             + len(unparsable) + len(too_deep))
    print(f"\n== {valid_count} valid, {len(invalid)} invalid, "
          f"{len(unknown) + len(disagreed)} unknown, {len(unparsable)} unparsable, "
          f"{len(too_deep)} too deep ({total} seeds) via {', '.join(impls)} ==")
    # Invalid seeds are expected (whole categories are deliberately malformed),
    # so they are reported, not fatal. Only a seed nobody could judge is a
    # failure of the check itself.
    return 1 if (unknown or disagreed or unparsable) else 0


if __name__ == "__main__":
    sys.exit(main())
