# Maple Mono, cut down to ASCII

`maple-mono-regular.ttf` is [Maple Mono NF CN][src] 7.900 with everything but
printable ASCII removed: 20 MB of Nerd Font and CJK coverage down to 13 KB, the
part a graph actually letters. Axis labels, legends and the SI suffix ladder
(`y z a f p n u m k M G T P E Z Y`) are all inside U+0020–U+007E.

The web pages use the same cut in WOFF2, under `internal/site/fonts/`.

A title or legend outside that range draws as an empty box. The font it
replaced, Go Mono, had no CJK either, so nothing that used to render stopped.

Rebuild from an installed copy of the full font with:

    pyftsubset MapleMono-NF-CN-Regular.ttf \
      --unicodes=U+0020-007E --layout-features='' --no-hinting \
      --notdef-outline --name-IDs=0,1,2,3,4,5,6,7,13,14 \
      --output-file=maple-mono-regular.ttf

Ligatures go with `--layout-features=''`: the renderer applies no shaping, so
keeping them would only have made the pages disagree with the drawings.

Licensed under the SIL Open Font License 1.1 — see `OFL.txt`. The notice also
travels inside the file, in its name table.

[src]: https://github.com/subframe7536/maple-font
