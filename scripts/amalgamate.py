"""Flatten code/go plus the gen symbols it uses into one runnable file.

The point is that the published number has a file behind it that a reader can
run, so this is a mechanical transform of the real package rather than a second
implementation: no hand-transcribed source, and the result is byte-compared
against the same reference output before it is published.
"""

import re
import pathlib
import sys

# Anchored on the script's own parent, because scripts/ and code/ are siblings in both layouts this study ships in.
REPO = pathlib.Path(__file__).resolve().parent.parent
GO, GEN = REPO / "code/go", REPO / "code/gen"
OUT = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else REPO / "1brc.go"

SOURCES = ("main.go", "reader.go", "table.go", "kernel.go", "batch.go")
# Only these gen symbols are reachable from the runtime path; the rest of the package is generation and the slow reference.
WANT_GEN = ["Tenths", "MinTenths", "RoundToTenths", "AppendTenths", "Mean", "Accumulator", "WriteResult"]


def strip_head(path, drop_imports=()):
    """Return (body without package clause and import block, set of import lines)."""
    text = path.read_text()
    text = re.sub(r"\A(?://[^\n]*\n)*package \w+\n", "", text)
    imports = set()

    def eat(match):
        for line in match.group(1).split("\n"):
            line = line.strip()
            if not line or line.startswith("//") or any(d in line for d in drop_imports):
                continue
            imports.add(line)
        return ""

    text = re.sub(r"^import \(\n(.*?)\n\)\n", eat, text, count=1, flags=re.S | re.M)
    return text.strip("\n"), imports


def top_level_decls(body):
    """Yield (name, source) per top-level declaration, doc comment attached."""
    decls, current, name = [], [], None
    for line in body.split("\n"):
        start = re.match(r"^(?:func|type|const|var)\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)?", line)
        if start:
            if current:
                doc = []
                while current and current[-1].startswith("//"):
                    doc.insert(0, current.pop())
                decls.append((name, "\n".join(current).strip("\n")))
                current = doc
            name = start.group(1)
        current.append(line)
    decls.append((name, "\n".join(current).strip("\n")))
    return [(n, s) for n, s in decls if s]


def declares(name, source, wanted):
    """A grouped const or var block has no name of its own, so look inside it too."""
    if name in wanted:
        return True
    return any(re.match(r"^\t(%s)\b" % "|".join(wanted), line) for line in source.split("\n"))


gen_src, gen_imports = [], set()
for filename in ("tenths.go", "reference.go"):
    body, imports = strip_head(GEN / filename)
    for name, source in top_level_decls(body):
        if declares(name, source, WANT_GEN):
            gen_src.append(source)
            gen_imports |= imports

missing = [w for w in WANT_GEN if not any(re.search(r"\b%s\b" % w, s) for s in gen_src)]
if missing:
    sys.exit("gen declarations not extracted: " + ", ".join(missing))

parts, imports = [], set()
for filename in SOURCES:
    body, file_imports = strip_head(GO / filename, drop_imports=("code/asm", "code/gen"))
    parts.append("// ---- code/go/%s ----\n\n%s" % (filename, body))
    imports |= file_imports

merged = "\n\n".join(parts)
merged += "\n\n// ---- code/gen: the temperature arithmetic and the output format both sides must agree on ----\n\n"
merged += "\n\n".join(gen_src)
merged = re.sub(r"\bgen\.", "", merged)
imports = {i for i in (imports | gen_imports) if "code/asm" not in i and "code/gen" not in i}

# The NEON batch arm is the one thing a single file cannot carry, since it calls into a Plan 9 assembly module.
neon = re.search(
    r"(?:^//[^\n]*\n)*func \(t \*table\) foldBatchNEON\(data \[\]byte, base int64\) \(int, error\) \{.*?\n\}\n",
    merged,
    re.S | re.M,
)
if not neon:
    sys.exit("foldBatchNEON not found; the drop rule needs updating")
merged = merged.replace(
    neon.group(0),
    "// foldBatchNEON is the one arm that cannot cross into a single file, since its 32-byte compare is a call into the Plan 9 assembly module.\n"
    "func (t *table) foldBatchNEON(data []byte, base int64) (int, error) {\n"
    '\treturn 0, fmt.Errorf("-kernel batch-neon needs the assembly module, so run it from code/go rather than this file")\n'
    "}\n",
)

DOC = "\n".join([
    "// Command 1brc aggregates a 1BRC measurements file into min/mean/max per station: go run 1brc.go -in measurements.txt",
    "//",
    "// Generated from code/go by scripts/amalgamate.py, so change that package rather than this file.",
    "// The batch-neon arm is stubbed here because one file cannot link a Plan 9 assembly module.",
])

header = "%s\npackage main\n\nimport (\n%s\n)\n\n" % (
    DOC,
    "\n".join("\t" + i for i in sorted(imports)),
)

OUT.write_text(header + merged + "\n")
print("wrote %s" % OUT)
print("imports: %s" % ", ".join(sorted(imports)))
