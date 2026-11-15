# JavaScript embedded in the app

`script.go` embeds these files with `go:embed`.

| File | What | Source |
| --- | --- | --- |
| `prelude.js` | The script API (`pm`, `bru`, `req`, `res`, `console`, `test`), written for this app | here |
| `crypto-js.min.js` | crypto-js 4.2.0, the `CryptoJS` global and `require('crypto-js')` | <https://cdnjs.cloudflare.com/ajax/libs/crypto-js/4.2.0/crypto-js.min.js> |
| `chai.min.js` | chai 4.5.0, `pm.expect`, `expect` and `require('chai')` | <https://cdnjs.cloudflare.com/ajax/libs/chai/4.5.0/chai.min.js> |

Both libraries are MIT licensed; their licenses are `crypto-js.LICENSE` and `chai.LICENSE`.
The files are unmodified. SHA-256, to check an update against:

```
769a555de553babc35a3338f344dd7aa16260c93cea2c7db290707c90484e7cc  crypto-js.min.js
73dc9d4bf3749ab1b6b1a20d04a020435cac10441a46600532a5c0bafe363084  chai.min.js
```

chai stays on 4.x: chai 5 is an ES module, which the interpreter does not load.
