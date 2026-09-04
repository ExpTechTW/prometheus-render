#!/usr/bin/env python3
"""Audit legend swatches: every graph's legend must name each series once.

A selector whose label set changed partway through the window comes back as
several series sharing one legend name, which also shifts every colour out of
its role. That is invisible in the tool's own output and only shows up in the
rendered legend, so it is checked there.
"""
import glob
import struct
import sys
import zlib
from collections import Counter


def decode_png(path):
    """Return (width, height, rows) for an 8-bit non-interlaced PNG.

    Written out rather than pulled from a library so the check shares no code
    with the renderer it is checking.
    """
    data = open(path, "rb").read()
    assert data[:8] == b"\x89PNG\r\n\x1a\n", "not a PNG"
    pos, idat, width, channels = 8, b"", None, 3
    while pos < len(data):
        (length,) = struct.unpack(">I", data[pos : pos + 4])
        ctype = data[pos + 4 : pos + 8]
        body = data[pos + 8 : pos + 8 + length]
        if ctype == b"IHDR":
            width, height, depth, colour, _, _, interlace = struct.unpack(">IIBBBBB", body)
            assert depth == 8 and interlace == 0, f"unsupported PNG {depth=} {interlace=}"
            channels = 3 if colour == 2 else 4
        elif ctype == b"IDAT":
            idat += body
        elif ctype == b"IEND":
            break
        pos += 12 + length

    raw = zlib.decompress(idat)
    stride = width * channels
    rows, prev, at = [], bytearray(stride), 0
    for _ in range(height):
        filt = raw[at]
        line = bytearray(raw[at + 1 : at + 1 + stride])
        at += 1 + stride
        for i in range(stride):
            a = line[i - channels] if i >= channels else 0
            b = prev[i]
            c = prev[i - channels] if i >= channels else 0
            if filt == 1:
                line[i] = (line[i] + a) & 0xFF
            elif filt == 2:
                line[i] = (line[i] + b) & 0xFF
            elif filt == 3:
                line[i] = (line[i] + (a + b) // 2) & 0xFF
            elif filt == 4:
                p = a + b - c
                pa, pb, pc = abs(p - a), abs(p - b), abs(p - c)
                pr = a if (pa <= pb and pa <= pc) else (b if pb <= pc else c)
                line[i] = (line[i] + pr) & 0xFF
        rows.append([tuple(line[i : i + 3]) for i in range(0, stride, channels)])
        prev = line
    return width, height, rows

# The swatch column is found rather than assumed: it moves with --zoom, and a
# hard-coded offset silently reports every legend as empty. The bevel runs down
# the left edge, so the scan starts past it.
BEVEL = 6


def swatch_span(rows, h, w):
    """The leftmost run of one saturated colour in the legend strip."""
    strip = range(h // 2, h)
    bg = Counter(rows[y][x] for y in strip for x in range(BEVEL, min(w, 120))).most_common(1)[0][0]
    best = None
    for y in strip:
        x = BEVEL
        while x < min(w, 120):
            c = rows[y][x]
            if c != bg and max(c) - min(c) > 40:
                run = x
                while run < w and rows[y][run] == c:
                    run += 1
                if run - x >= 3 and (best is None or x < best[0]):
                    best = (x, run)
                break
            x += 1
    return best


MRTG = {
    (0, 204, 0): "C1 rx",
    (0, 0, 255): "C2 tx",
    (0, 102, 0): "C3 rx-peak",
    (255, 0, 255): "C4 tx-peak",
}


def swatches(path):
    w, h, rows = decode_png(path)
    span = swatch_span(rows, h, w)
    if span is None:
        return []
    x0, x1 = span
    out, prev = [], None
    for y in range(h // 2, h):
        px = {rows[y][x] for x in range(x0, x1)}
        c = px.pop() if len(px) == 1 else None
        if c and c != prev and abs(max(c) - min(c)) > 40:
            out.append(c)
        prev = c
    return out


def main():
    bad = 0
    hist = Counter()
    for path in sorted(glob.glob("out/*/*/*/*/*.png")):
        sw = swatches(path)
        hist[len(sw)] += 1
        dupes = [c for c, n in Counter(sw).items() if n > 1]
        if dupes or len(sw) not in (1, 2, 4):
            print(f"  {path}: {[MRTG.get(c, c) for c in sw]}")
            bad += 1
    print(f"  swatch-count histogram: {dict(sorted(hist.items()))}")
    print(f"  legends with a repeated or unexpected series: {bad}")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
