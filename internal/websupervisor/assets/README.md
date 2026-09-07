# Embedded terminal assets

The dashboard loads no assets from a CDN and needs no Node.js runtime.
Vendored release files are unmodified and retain their MIT licenses:

- `@xterm/xterm` 6.0.0: https://github.com/xtermjs/xterm.js/releases/tag/6.0.0
- `@xterm/addon-fit` 0.11.0: https://www.npmjs.com/package/@xterm/addon-fit/v/0.11.0

Acquisition: `npm pack @xterm/xterm@6.0.0 @xterm/addon-fit@0.11.0`;
extract `lib/xterm.js`, `css/xterm.css`, `lib/addon-fit.js`, and both licenses.
Package integrity (SHA-512, base64):

```
xterm: TQwDdQGtwwDt+2cgKDLn0IRaSxYu1tSUjgKarSDkUM0ZNiSRXFpjxEsvc/Zgc5kq5omJ+V0a8/kIM2WD3sMOYg==
addon-fit: jYcgT6xtVYhnhgxh3QgYDnnNMYTcf8ElbxxFzX0IZo+vabQqSPAjC3c1wJrKB5E19VwQei89QCiZZP86DCPF7g==
```

SHA-256 of embedded files:

```
14903579ff54664cd72f8e8699e6961a6272c21863ec1c3b118cdc8af5d4a972  xterm.js
854a7c0fb70e8b1a083c16797ab827299fb18744f5ad34f227b48337e33293c6  xterm.css
ba3ea256ce0620a0992a197d6c9baea64823fc93d8da07a9e366ca9943c18527  addon-fit.js
```

Source maps are not embedded. `app.js`, `app.css`, and `index.html` are the
project-owned dashboard, served with a restrictive Content Security Policy.
