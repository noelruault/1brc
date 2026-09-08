"""Derive the idiomatic and portable tiers from the unrestricted one.

Patching rather than retyping keeps the kernels, the table and the reader byte-identical across the three, so a difference between two tiers is only ever the restriction that defines it.
"""

import pathlib
import re
import sys

# Anchored on the script's own parent, because scripts/ and go/ are siblings.
REPO = pathlib.Path(__file__).resolve().parent.parent
SRC = pathlib.Path(sys.argv[1]) if len(sys.argv) > 1 else REPO / "go/unrestricted/1brc.go"
OUT_IDIOMATIC = pathlib.Path(sys.argv[2]) if len(sys.argv) > 2 else REPO / "go/idiomatic/1brc.go"
OUT_PORTABLE = pathlib.Path(sys.argv[3]) if len(sys.argv) > 3 else REPO / "go/portable/1brc.go"

src = SRC.read_text()


def cut(text, start, end_marker):
    """Remove the block from the line starting with `start` up to (not including) `end_marker`."""
    i = text.index(start)
    j = text.index(end_marker, i)
    return text[:i] + text[j:]


SLICE_FOLD = '''// fold re-slices at every row, so every index is bounds-checked by the compiler rather than by the loop.
// It stops maxRow short of the end so the 8-byte over-fetch below stays inside the buffer; foldTail closes the rest without ever over-reading.
func (t *table) fold(data []byte, base int64) error {
	pos := 0
	for pos+maxRow <= len(data) {
		sep, semi := indexDelim(data[pos:])
		if sep < 0 || !semi {
			return rowError(base+int64(pos), data[pos:])
		}
		if pos+sep+9 > len(data) {
			break
		}
		v, next, ok := parseTemp(binary.LittleEndian.Uint64(data[pos+sep+1:]))
		if !ok || uint32(v+999) > 1998 {
			return rowError(base+int64(pos), data[pos:])
		}
		name := data[pos : pos+sep]
		kw := maskWord(binary.LittleEndian.Uint64(data[pos:]), sep)
		if !t.update(mixWord(kw), name, v) {
			return t.fullError(base + int64(pos))
		}
		pos += sep + 1 + next
	}
	return t.foldTail(data, pos, base)
}
'''

# The pointer walk and the pointer scan are the whole of what `unsafe` buys, so both go and the slice equivalents stay.
text = re.sub(
    r"// fold walks the buffer with unsafe\.Add.*?\n\treturn t\.foldTail\(data, pos, base\)\n\}\n",
    SLICE_FOLD,
    src,
    count=1,
    flags=re.S,
)
if text == src:
    sys.exit("pointer fold not found")

text = cut(text, "// indexDelimAt finds the first", "// indexDelim is indexDelimAt over a slice")
text = text.replace(
    "// indexDelim is indexDelimAt over a slice, for the tail that must not over-read.",
    "// indexDelim finds the first ';' or '\\n' eight bytes at a time and reports which one it found.\n"
    "//\n"
    "// A borrow chain can set a lane's high bit ABOVE a match but never below one, so the lowest set bit is always the first match.\n"
    "// Both needles are scanned, not just ';', because a row with no separator would otherwise run its name into the NEXT row and fold a station that does not exist; the reference rejects that row, so this has to as well.",
)
text = text.replace('\t"unsafe"\n', "")
text = text.replace(
    "// This is the unrestricted tier: an unsafe pointer walk over the rows and darwin's F_NOCACHE on the read.",
    "// This is the idiomatic tier: the standard library only, `syscall` included, so the rows are walked as slices and the read still sets darwin's F_NOCACHE.",
)
OUT_IDIOMATIC.write_text(text)

# Portable drops the one call that is not portable: F_NOCACHE is a darwin-only constant, and skipping it removes the syscall package entirely.
portable = text.replace(
    "// This is the idiomatic tier: the standard library only, `syscall` included, so the rows are walked as slices and the read still sets darwin's F_NOCACHE.",
    "// This is the portable idiomatic tier: no OS-specific call at all, so the read goes through the page cache and no `syscall` import remains.",
)
portable = re.sub(
    r"\tif _, _, e := syscall\.Syscall\(syscall\.SYS_FCNTL, f\.Fd\(\), uintptr\(fNoCache\), 1\); e != 0 \{\n"
    r"\t\treturn nil, fmt\.Errorf\(\"fcntl F_NOCACHE: %w\", e\)\n\t\}\n",
    "",
    portable,
    count=1,
)
portable = re.sub(
    r"// fNoCache is darwin's F_NOCACHE\..*?\nconst fNoCache = 48\n\n",
    "",
    portable,
    count=1,
    flags=re.S,
)
portable = portable.replace('\t"syscall"\n', "")
for gone in ("syscall.", "fNoCache", "unsafe."):
    if gone in portable:
        sys.exit("portable tier still references %s" % gone)
OUT_PORTABLE.write_text(portable)

print("wrote %s and %s" % (OUT_IDIOMATIC, OUT_PORTABLE))
