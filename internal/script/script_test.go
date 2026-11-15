package script

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"hash"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"http_gui/internal/httpx"
	"http_gui/internal/model"
	"http_gui/internal/vars"
)

func on(key, value string) model.KV { return model.KV{Key: key, Value: value, Enabled: true} }

// newRun returns a pre-request run of a script on a request with a
// variable in its URL, a parameter, a header and a JSON body.
func newRun(source string) Run {
	return Run{
		Name:   "pre-request",
		Script: model.Script{Lang: "js", Source: source},
		Request: &model.Request{
			ID: "r1", Name: "Create order", Method: "POST",
			URL:     "{{base}}/orders?page=1",
			Params:  []model.KV{on("q", "a b")},
			Headers: []model.KV{on("X-Old", "1"), on("Accept", "text/plain")},
			Body:    model.Body{Kind: model.BodyJSON, Raw: `{"user": "{{user}}"}`},
		},
		Scopes: &vars.Scopes{
			Environment: map[string]string{"base": "https://api.example.com", "user": "ada", "gone": "x"},
			Global:      map[string]string{"company": "acme"},
		},
	}
}

func exec(t *testing.T, in Run) Output {
	t.Helper()
	out, err := Exec(context.Background(), in)
	if err != nil {
		t.Fatalf("the script failed: %v\nconsole: %v", err, out.Console)
	}
	return out
}

// run runs a pre-request script and returns what it left in the run's own
// variables, where the tests' scripts put their results.
func run(t *testing.T, source string) map[string]string {
	t.Helper()
	in := newRun(source)
	exec(t, in)
	return in.Scopes.Runtime
}

func TestVariables(t *testing.T) {
	in := newRun(`
		pm.environment.set('token', 'abc');
		pm.environment.unset('gone');
		pm.collectionVariables.set('count', 42);
		pm.globals.set('object', {a: 1});
		pm.variables.set('local', pm.environment.get('token') + '!');
		pm.variables.set('throughScopes', pm.variables.get('company') + '/' + pm.variables.get('user'));
		pm.variables.set('unknown', typeof pm.environment.get('nope'));
		pm.variables.set('has', pm.environment.has('token') + ',' + pm.environment.has('nope') + ',' + pm.variables.has('company'));
		pm.variables.set('replaced', pm.variables.replaceIn('{{base}}/x/{{nope}}'));
		pm.variables.set('all', JSON.stringify(pm.environment.toObject()));
	`)
	exec(t, in)
	s := in.Scopes
	if s.Environment["token"] != "abc" || s.Collection["count"] != "42" || s.Global["object"] != `{"a":1}` {
		t.Errorf("scopes after set: %+v", s)
	}
	if _, ok := s.Environment["gone"]; ok {
		t.Error("unset left the variable")
	}
	want := map[string]string{
		"local":         "abc!",
		"throughScopes": "acme/ada",
		"unknown":       "undefined",
		"has":           "true,false,true",
		"replaced":      "https://api.example.com/x/{{nope}}",
	}
	for name, value := range want {
		if got := s.Runtime[name]; got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	var all map[string]string
	json.Unmarshal([]byte(s.Runtime["all"]), &all)
	if wantAll := map[string]string{"base": "https://api.example.com", "token": "abc", "user": "ada"}; !reflect.DeepEqual(all, wantAll) {
		t.Errorf("toObject = %s, want %v", s.Runtime["all"], wantAll)
	}
}

// A pre-request script changes the request that is sent, as a signature
// header added to it, and never the one the user wrote.
func TestPreRequestChangesTheRequest(t *testing.T) {
	in := newRun(`
		pm.variables.set('seen', [pm.request.method, pm.request.url.toString(), pm.request.headers.get('x-old'),
			pm.request.headers.has('Nope'), pm.request.body.mode, pm.request.body.raw, pm.info.eventName].join('|'));
		pm.request.method = 'put';
		pm.request.url = pm.request.url + '&extra=1';
		pm.request.headers.add({key: 'X-New', value: 'n'});
		pm.request.headers.upsert({key: 'accept', value: 'application/json'});
		pm.request.headers.upsert({key: 'X-Count', value: 3});
		pm.request.headers.remove('x-old');
		pm.request.body.raw = JSON.stringify({changed: true});
	`)
	before := *in.Request
	out := exec(t, in)

	if got, want := in.Scopes.Runtime["seen"], `POST|{{base}}/orders?page=1|1|false|raw|{"user": "{{user}}"}|prerequest`; got != want {
		t.Errorf("the script saw %q, want %q", got, want)
	}
	r := out.Request
	if r.Method != "put" || r.URL != "{{base}}/orders?page=1&extra=1" || r.Body.Raw != `{"changed":true}` {
		t.Errorf("request after the script: %+v", r)
	}
	var headers []string
	for _, h := range r.Headers {
		headers = append(headers, h.Key+"="+h.Value)
	}
	if want := []string{"Accept=application/json", "X-New=n", "X-Count=3"}; !reflect.DeepEqual(headers, want) {
		t.Errorf("headers %v, want %v", headers, want)
	}
	if !reflect.DeepEqual(*in.Request, before) {
		t.Error("the script changed the request as the user wrote it")
	}
}

// What a signature covers is the URL as it is sent: variables resolved,
// parameters in the query.
func TestURLParts(t *testing.T) {
	got := run(t, `
		const u = pm.request.url;
		pm.variables.set('parts', [u.getHost(), u.getPath(), u.getQueryString()].join(' '));
		pm.request.url = 'localhost:8080';
		pm.variables.set('bare', pm.request.url.getHost() + ' ' + pm.request.url.getPath());
	`)
	if want := "api.example.com /orders page=1&q=a+b"; got["parts"] != want {
		t.Errorf("parts %q, want %q", got["parts"], want)
	}
	if want := "localhost:8080 /"; got["bare"] != want {
		t.Errorf("bare %q, want %q", got["bare"], want)
	}
}

// The hashes are Go's: a script's digest must be the one a server
// computes.
func TestHashesMatchGo(t *testing.T) {
	got := run(t, `
		const crypto = require('crypto');
		for (const alg of ['md5', 'sha1', 'sha256', 'sha384', 'sha512']) {
			pm.variables.set(alg, crypto.createHash(alg).update('ab').update('c').digest('hex'));
			pm.variables.set('hmac-' + alg, crypto.createHmac(alg, 'key').update('abc').digest('hex'));
		}
		pm.variables.set('base64', crypto.createHash('SHA-256').update('abc').digest('base64'));
		pm.variables.set('buffer', crypto.createHash('sha256').update(Buffer.from('616263', 'hex')).digest().toString('hex'));
		pm.variables.set('encoded', crypto.createHash('sha256').update('YWJj', 'base64').digest('hex'));
		// RFC 4231, test case 2.
		pm.variables.set('rfc4231', crypto.createHmac('sha256', 'Jefe').update('what do ya want for nothing?').digest('hex'));
		pm.variables.set('node', require('node:crypto').createHash('sha256').update('abc').digest('hex'));
	`)
	hashes := map[string]func() hash.Hash{
		"md5": md5.New, "sha1": sha1.New, "sha256": sha256.New, "sha384": sha512.New384, "sha512": sha512.New,
	}
	for alg, newHash := range hashes {
		h := newHash()
		h.Write([]byte("abc"))
		if want := hex.EncodeToString(h.Sum(nil)); got[alg] != want {
			t.Errorf("%s = %s, want %s", alg, got[alg], want)
		}
		m := hmac.New(newHash, []byte("key"))
		m.Write([]byte("abc"))
		if want := hex.EncodeToString(m.Sum(nil)); got["hmac-"+alg] != want {
			t.Errorf("hmac %s = %s, want %s", alg, got["hmac-"+alg], want)
		}
	}
	sum := sha256.Sum256([]byte("abc"))
	if got["base64"] != base64.StdEncoding.EncodeToString(sum[:]) {
		t.Errorf("base64 digest %q", got["base64"])
	}
	for _, name := range []string{"buffer", "encoded", "node"} {
		if got[name] != hex.EncodeToString(sum[:]) {
			t.Errorf("%s = %s, want the SHA-256 of abc", name, got[name])
		}
	}
	if want := "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"; got["rfc4231"] != want {
		t.Errorf("RFC 4231 vector = %s, want %s", got["rfc4231"], want)
	}
}

func pemOf(t *testing.T, kind string, der []byte, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}))
}

// keys makes a key pair of each kind and returns them as PEM in variables,
// as a user keeps theirs.
func keys(t *testing.T) (rsaKey *rsa.PrivateKey, ecKey *ecdsa.PrivateKey, edKey ed25519.PrivateKey, env map[string]string) {
	t.Helper()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	env = map[string]string{"rsaPKCS1": pemOf(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(rsaKey), nil)}
	var der []byte
	der, err = x509.MarshalPKCS8PrivateKey(rsaKey)
	env["rsaPrivate"] = pemOf(t, "PRIVATE KEY", der, err)
	der, err = x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	env["rsaPublic"] = pemOf(t, "PUBLIC KEY", der, err)
	der, err = x509.MarshalECPrivateKey(ecKey)
	env["ecPrivate"] = pemOf(t, "EC PRIVATE KEY", der, err)
	der, err = x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	env["ecPublic"] = pemOf(t, "PUBLIC KEY", der, err)
	der, err = x509.MarshalPKCS8PrivateKey(edKey)
	env["edPrivate"] = pemOf(t, "PRIVATE KEY", der, err)
	der, err = x509.MarshalPKIXPublicKey(edKey.Public())
	env["edPublic"] = pemOf(t, "PUBLIC KEY", der, err)
	return rsaKey, ecKey, edKey, env
}

func decode(t *testing.T, b64 string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("not base64: %q", b64)
	}
	return b
}

// A signature made in a script is checked by a server with the public key:
// each kind must verify with Go's own crypto, not only with the script's.
func TestSignaturesVerifyInGo(t *testing.T) {
	rsaKey, ecKey, edKey, env := keys(t)
	in := newRun(`
		const crypto = require('crypto');
		const key = name => pm.environment.get(name);
		const data = 'POST/orders{"n":1}';
		pm.variables.set('pkcs1', crypto.createSign('RSA-SHA256').update('POST/orders').update('{"n":1}').sign(key('rsaPrivate'), 'base64'));
		pm.variables.set('pkcs1Key', crypto.createSign('sha256').update(data).sign(key('rsaPKCS1'), 'base64'));
		pm.variables.set('pss', crypto.sign('sha256', data, {
			key: key('rsaPrivate'),
			padding: crypto.constants.RSA_PKCS1_PSS_PADDING,
			saltLength: crypto.constants.RSA_PSS_SALTLEN_DIGEST,
		}).toString('base64'));
		pm.variables.set('ecDER', crypto.createSign('SHA256').update(data).sign(key('ecPrivate'), 'base64'));
		pm.variables.set('ecRaw', crypto.sign('sha256', Buffer.from(data), {key: key('ecPrivate'), dsaEncoding: 'ieee-p1363'}).toString('base64'));
		pm.variables.set('ed', crypto.sign(null, Buffer.from(data), key('edPrivate')).toString('base64'));
	`)
	in.Scopes.Environment = env
	exec(t, in)
	got := in.Scopes.Runtime

	data := []byte(`POST/orders{"n":1}`)
	digest := sha256.Sum256(data)
	for _, name := range []string{"pkcs1", "pkcs1Key"} {
		if err := rsa.VerifyPKCS1v15(&rsaKey.PublicKey, crypto.SHA256, digest[:], decode(t, got[name])); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}
	if err := rsa.VerifyPSS(&rsaKey.PublicKey, crypto.SHA256, digest[:], decode(t, got["pss"]), opts); err != nil {
		t.Errorf("PSS: %v", err)
	}
	if !ecdsa.VerifyASN1(&ecKey.PublicKey, digest[:], decode(t, got["ecDER"])) {
		t.Error("the ECDSA signature in DER does not verify")
	}
	raw := decode(t, got["ecRaw"])
	if len(raw) != 64 || !ecdsa.Verify(&ecKey.PublicKey, digest[:], new(big.Int).SetBytes(raw[:32]), new(big.Int).SetBytes(raw[32:])) {
		t.Errorf("the ECDSA signature as r and s (%d bytes) does not verify", len(raw))
	}
	if !ed25519.Verify(edKey.Public().(ed25519.PublicKey), data, decode(t, got["ed"])) {
		t.Error("the Ed25519 signature does not verify")
	}
}

// The reverse, for scripts that check a response's signature: what Go
// signed verifies in a script, and a changed message does not.
func TestScriptsVerifyGoSignatures(t *testing.T) {
	rsaKey, ecKey, edKey, env := keys(t)
	data := []byte("signed by the server")
	digest := sha256.Sum256(data)
	sig := func(b []byte, err error) string {
		if err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(b)
	}
	env["sigRSA"] = sig(rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:]))
	env["sigPSS"] = sig(rsa.SignPSS(rand.Reader, rsaKey, crypto.SHA256, digest[:], nil))
	env["sigEC"] = sig(ecdsa.SignASN1(rand.Reader, ecKey, digest[:]))
	env["sigEd"] = sig(ed25519.Sign(edKey, data), nil)

	in := newRun(`
		const crypto = require('crypto');
		const v = name => pm.environment.get(name);
		const data = 'signed by the server';
		const check = (text) => [
			crypto.createVerify('RSA-SHA256').update(text).verify(v('rsaPublic'), v('sigRSA'), 'base64'),
			crypto.verify('sha256', text, {key: v('rsaPublic'), padding: crypto.constants.RSA_PKCS1_PSS_PADDING}, Buffer.from(v('sigPSS'), 'base64')),
			crypto.createVerify('sha256').update(text).verify(v('ecPublic'), v('sigEC'), 'base64'),
			crypto.verify(null, Buffer.from(text), v('edPublic'), Buffer.from(v('sigEd'), 'base64')),
			// A private key holds its public one.
			crypto.createVerify('sha256').update(text).verify(v('rsaPrivate'), v('sigRSA'), 'base64'),
		].join(',');
		pm.variables.set('good', check(data));
		pm.variables.set('tampered', check(data + '!'));
	`)
	in.Scopes.Environment = env
	exec(t, in)
	if got := in.Scopes.Runtime["good"]; got != "true,true,true,true,true" {
		t.Errorf("verifying Go's signatures: %s", got)
	}
	if got := in.Scopes.Runtime["tampered"]; got != "false,false,false,false,false" {
		t.Errorf("verifying a changed message: %s", got)
	}
}

func TestRSAEncryption(t *testing.T) {
	rsaKey, _, _, env := keys(t)
	ct, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &rsaKey.PublicKey, []byte("from go"), nil)
	if err != nil {
		t.Fatal(err)
	}
	env["fromGo"] = base64.StdEncoding.EncodeToString(ct)
	in := newRun(`
		const crypto = require('crypto');
		pm.variables.set('toGo', crypto.publicEncrypt(pm.environment.get('rsaPublic'), Buffer.from('from script')).toString('base64'));
		pm.variables.set('read', crypto.privateDecrypt({key: pm.environment.get('rsaPrivate'), oaepHash: 'sha256'},
			Buffer.from(pm.environment.get('fromGo'), 'base64')).toString());
		pm.variables.set('random', crypto.randomBytes(16).toString('hex') + ' ' + crypto.randomUUID());
	`)
	in.Scopes.Environment = env
	exec(t, in)
	got := in.Scopes.Runtime

	// Node's default, which servers expect: OAEP with SHA-1.
	plain, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, rsaKey, decode(t, got["toGo"]), nil)
	if err != nil || string(plain) != "from script" {
		t.Errorf("Go decrypted %q, %v", plain, err)
	}
	if got["read"] != "from go" {
		t.Errorf("the script decrypted %q", got["read"])
	}
	if len(got["random"]) != 32+1+36 {
		t.Errorf("random %q", got["random"])
	}
}

func TestKeyErrorsSayWhy(t *testing.T) {
	_, _, _, env := keys(t)
	tests := []struct{ source, want string }{
		{`require('crypto').createSign('sha256').update('x').sign('not a key')`, "not PEM"},
		{`require('crypto').createSign('sha256').update('x').sign(pm.environment.get('rsaPublic'))`, "private key"},
		{`require('crypto').createHash('sha3').update('x')`, "unknown digest"},
		{`require('crypto').createSign('sha256').update('x').sign(pm.environment.get('edPrivate'))`, "Ed25519"},
	}
	for _, tt := range tests {
		in := newRun(tt.source)
		in.Scopes.Environment = env
		if _, err := Exec(context.Background(), in); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s\n  error %v, want one saying %q", tt.source, err, tt.want)
		}
	}
}

// Postman's scripts call CryptoJS, which must give what Go gives, and its
// encryption needs random bytes from the crypto module.
func TestCryptoJS(t *testing.T) {
	got := run(t, `
		const crypto = require('crypto');
		pm.variables.set('hmac', CryptoJS.HmacSHA256('message', 'secret').toString(CryptoJS.enc.Hex));
		pm.variables.set('node', crypto.createHmac('sha256', 'secret').update('message').digest('hex'));
		pm.variables.set('base64', CryptoJS.enc.Base64.stringify(CryptoJS.SHA256('abc')));
		pm.variables.set('required', String(require('crypto-js') === CryptoJS));
		const box = CryptoJS.AES.encrypt('hidden', 'passphrase').toString();
		pm.variables.set('aes', CryptoJS.AES.decrypt(box, 'passphrase').toString(CryptoJS.enc.Utf8));
		pm.variables.set('b64', btoa('hello') + ' ' + atob('aGVsbG8=') + ' ' + Buffer.from('hello').toString('base64'));
	`)
	m := hmac.New(sha256.New, []byte("secret"))
	m.Write([]byte("message"))
	if want := hex.EncodeToString(m.Sum(nil)); got["hmac"] != want || got["node"] != want {
		t.Errorf("HMAC: CryptoJS %s, crypto %s, Go %s", got["hmac"], got["node"], want)
	}
	sum := sha256.Sum256([]byte("abc"))
	if got["base64"] != base64.StdEncoding.EncodeToString(sum[:]) {
		t.Errorf("CryptoJS SHA-256 in base64: %s", got["base64"])
	}
	if got["required"] != "true" || got["aes"] != "hidden" {
		t.Errorf("required %s, AES round trip %q", got["required"], got["aes"])
	}
	if got["b64"] != "aGVsbG8= hello aGVsbG8=" {
		t.Errorf("base64 helpers: %q", got["b64"])
	}
}

func TestTypeScript(t *testing.T) {
	in := newRun(`
		interface Order { id: number; tags?: string[] }
		enum Kind { Signed = 'signed' }
		function total(orders: Order[]): number {
			return orders.reduce((sum: number, o) => sum + o.id + (o.tags?.length ?? 0), 0);
		}
		class Signer {
			private prefix = 'sig';
			sign<T extends string>(value: T): string { return this.prefix + ':' + value; }
		}
		const orders: Order[] = [{ id: 7 }, { id: 1, tags: ['a', 'b'] }];
		const { id, ...rest } = { id: 1, kept: true } as const;
		const n = await Promise.resolve(total(orders));
		pm.variables.set('out', [Kind.Signed, n, new Signer().sign('x'), JSON.stringify(rest)].join(' '));
	`)
	in.Script.Lang = "ts"
	exec(t, in)
	if got, want := in.Scopes.Runtime["out"], `signed 10 sig:x {"kept":true}`; got != want {
		t.Errorf("out %q, want %q", got, want)
	}

	// The same text is not JavaScript.
	in.Script.Lang = "js"
	if _, err := Exec(context.Background(), in); err == nil {
		t.Error("TypeScript ran as JavaScript")
	}
}

// An error says where it is in the script as the user wrote it, not in
// the code it was turned into.
func TestErrorsNameTheLine(t *testing.T) {
	tests := []struct{ name, lang, source, want string }{
		{"syntax", "js", "const a = 1;\nconst b = ;\n", "pre-request.js:2:11"},
		{"syntax on the first line", "js", "const = 1;", "pre-request.js:1:7"},
		{"type syntax", "ts", "const a: number = 1;\nlet b: = 2;", "pre-request.ts:2:"},
		{"thrown", "js", "const a = 1;\n\nthrow new Error('boom');", "boom"},
		{"thrown, its line", "js", "const a = 1;\n\nthrow new Error('boom');", "pre-request.js:3"},
		{"thrown in TypeScript", "ts", "interface A { b: string }\ntype C = A;\n\n\nnull.x;", "pre-request.ts:5"},
		{"not an Error", "js", "throw 'just text';", "just text"},
		{"undefined name", "js", "\nnope();", "nope is not defined"},
		{"awaited rejection", "js", "await Promise.reject(new Error('later'));", "later"},
	}
	for _, tt := range tests {
		in := newRun(tt.source)
		in.Script.Lang = tt.lang
		_, err := Exec(context.Background(), in)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error %q, want it to contain %q", tt.name, err, tt.want)
		}
	}
}

func response() *httpx.Response {
	body := `{"token": "t-123", "items": [1, 2]}`
	return &httpx.Response{
		Status: "201 Created", StatusCode: 201,
		Headers:  http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"a=1", "b=2"}},
		Body:     []byte(body),
		Size:     int64(len(body)),
		Duration: 143 * time.Millisecond,
	}
}

func newPostRun(source string) Run {
	in := newRun(source)
	in.Name, in.Response = "post-response", response()
	return in
}

// The token flow: log in, keep the token the response has for the
// requests after it.
func TestPostResponseReadsTheResponse(t *testing.T) {
	in := newPostRun(`
		pm.environment.set('token', pm.response.json().token);
		pm.variables.set('seen', [pm.response.code, pm.response.status, pm.response.responseTime, pm.response.responseSize,
			pm.response.headers.get('content-type'), pm.response.headers.has('X-Nope'), pm.response.json().items.length,
			pm.response.text().length, pm.info.eventName].join('|'));
		pm.request.method = 'DELETE';
	`)
	out := exec(t, in)
	if in.Scopes.Environment["token"] != "t-123" {
		t.Errorf("token %q", in.Scopes.Environment["token"])
	}
	if got, want := in.Scopes.Runtime["seen"], "201|Created|143|35|application/json|false|2|35|test"; got != want {
		t.Errorf("the script saw %q, want %q", got, want)
	}
	if out.Request != in.Request {
		t.Error("a post-response script changed the request")
	}
}

func TestTests(t *testing.T) {
	out := exec(t, newPostRun(`
		pm.test('status is 201', () => pm.response.to.have.status(201));
		pm.test('status is 200', () => pm.response.to.have.status(200));
		pm.test('reason', () => pm.response.to.have.status('Created'));
		pm.test('header', () => pm.response.to.have.header('content-type', 'application/json'));
		pm.test('no such header', () => pm.response.to.have.header('X-Nope'));
		pm.test('success', () => pm.response.to.be.success);
		pm.test('ok is 200 alone', () => pm.response.to.be.ok);
		pm.test('chai passes', () => pm.expect(pm.response.json().items).to.have.lengthOf(2));
		pm.test('chai fails', () => pm.expect(pm.response.json().token).to.equal('other'));
		pm.test('throws', () => { throw new Error('mine'); });
		console.log('after the tests');
	`))
	want := []TestResult{
		{"status is 201", true, ""},
		{"status is 200", false, "expected response to have status code 200 but got 201"},
		{"reason", true, ""},
		{"header", true, ""},
		{"no such header", false, "expected response to have header with key 'X-Nope'"},
		{"success", true, ""},
		{"ok is 200 alone", false, "expected response to have status code 200 but got 201"},
		{"chai passes", true, ""},
		{"chai fails", false, "expected 't-123' to equal 'other'"},
		{"throws", false, "mine"},
	}
	if !reflect.DeepEqual(out.Tests, want) {
		t.Errorf("tests\n got %+v\nwant %+v", out.Tests, want)
	}
	// A failed test fails the test, not the script.
	if len(out.Console) != 1 {
		t.Errorf("the script stopped at a failed test: console %v", out.Console)
	}
}

func TestConsole(t *testing.T) {
	out := exec(t, newRun(`
		console.log('text', 42, {a: [1, 2]}, null, undefined, true);
		console.warn('careful');
		console.error(new Error('bad'));
	`))
	if len(out.Console) != 3 {
		t.Fatalf("console %+v", out.Console)
	}
	if got, want := out.Console[0], (LogLine{"log", `text 42 {"a":[1,2]} null undefined true`}); got != want {
		t.Errorf("log %+v, want %+v", got, want)
	}
	if out.Console[1] != (LogLine{"warn", "careful"}) {
		t.Errorf("warn %+v", out.Console[1])
	}
	if out.Console[2].Level != "error" || !strings.Contains(out.Console[2].Text, "bad") {
		t.Errorf("error %+v", out.Console[2])
	}
}

// Imported Bruno scripts run on the same functions.
func TestBruno(t *testing.T) {
	in := newRun(`
		const CryptoJS = require('crypto-js');
		bru.setVar('local', bru.getEnvVar('user') + '/' + bru.getGlobalEnvVar('company') + '/' + bru.hasVar('nope'));
		bru.setEnvVar('fromBru', bru.interpolate('{{base}}/x'));
		const body = req.getBody();
		body.signed = true;
		req.setBody(body);
		req.setHeader('X-Signature', CryptoJS.SHA256(req.getMethod() + req.getUrl()).toString());
		req.setHeader('accept', 'application/json');
		req.setMethod('PATCH');
		req.setUrl(req.getUrl() + '&v=2');
		bru.setVar('header', req.getHeader('X-OLD') + JSON.stringify(Object.keys(req.getHeaders())));
	`)
	out := exec(t, in)
	s := in.Scopes
	if s.Runtime["local"] != "ada/acme/false" || s.Environment["fromBru"] != "https://api.example.com/x" {
		t.Errorf("variables: runtime %v, environment %v", s.Runtime, s.Environment)
	}
	if s.Runtime["header"] != `1["X-Old","Accept","X-Signature"]` {
		t.Errorf("headers seen: %s", s.Runtime["header"])
	}
	r := out.Request
	sum := sha256.Sum256([]byte("POST{{base}}/orders?page=1"))
	if r.Method != "PATCH" || r.URL != "{{base}}/orders?page=1&v=2" || r.Body.Raw != `{"user":"{{user}}","signed":true}` ||
		r.Headers[1].Value != "application/json" || r.Headers[2].Value != hex.EncodeToString(sum[:]) {
		t.Errorf("request after the script: %+v", r)
	}

	post := newPostRun(`
		test('status', function () { expect(res.status).to.equal(201); });
		test('body', function () { expect(res.body.token).to.equal('nope'); });
		bru.setEnvVar('token', res.getBody().token);
		bru.setVar('seen', [res.getStatus(), res.statusText, res.headers['content-type'], res.getHeader('Content-Type'),
			res.headers['set-cookie'], res.getResponseTime()].join('|'));
	`)
	out = exec(t, post)
	if post.Scopes.Environment["token"] != "t-123" {
		t.Errorf("token %q", post.Scopes.Environment["token"])
	}
	if got, want := post.Scopes.Runtime["seen"], "201|Created|application/json|application/json|a=1, b=2|143"; got != want {
		t.Errorf("the script saw %q, want %q", got, want)
	}
	if len(out.Tests) != 2 || !out.Tests[0].Passed || out.Tests[1].Passed {
		t.Errorf("tests %+v", out.Tests)
	}
}

// A script that never ends must not hang the app.
func TestDeadline(t *testing.T) {
	in := newRun("console.log('before');\nwhile (true) {}")
	in.Timeout = 50 * time.Millisecond
	start := time.Now()
	out, err := Exec(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("error %v, want the deadline's", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("stopped after %v", elapsed)
	}
	if len(out.Console) != 1 {
		t.Errorf("what the script logged before it was stopped is lost: %v", out.Console)
	}
}

// Canceling the run stops its script too.
func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := Exec(ctx, newRun("while (true) {}"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want context.Canceled", err)
	}
}

// What one request's script leaves behind must not reach the next: each
// run starts clean.
func TestRunsShareNothing(t *testing.T) {
	run(t, `
		globalThis.leak = 1;
		Object.prototype.polluted = true;
		pm.leak = 1;
		require('crypto').createHash = () => 'replaced';
	`)
	got := run(t, `
		pm.variables.set('out', [typeof leak, typeof ({}).polluted, typeof pm.leak,
			typeof require('crypto').createHash('sha256').digest].join(','));
	`)
	if got["out"] != "undefined,undefined,undefined,function" {
		t.Errorf("the second run saw %s", got["out"])
	}
}

// Scripts come with imported collections, so they get no way to the
// machine: no files, no environment, no network, no timers.
func TestScriptsReachNothing(t *testing.T) {
	// Files that would load if require read the disk.
	dir := t.TempDir()
	module := filepath.Join(dir, "module.js")
	data := filepath.Join(dir, "data.json")
	if err := os.WriteFile(module, []byte("module.exports = 'read';"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, []byte(`{"read": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		"require(" + strconv.Quote(module) + ")",
		"require(" + strconv.Quote(data) + ")",
		"require(" + strconv.Quote(strings.TrimSuffix(module, ".js")) + ")",
		`require('fs')`,
		`require('child_process')`,
		`require('./local')`,
		`process.env.HOME`,
		`fetch('http://example.com')`,
		`setTimeout(function () {}, 0)`,
		`import('fs')`,
	} {
		if out, err := Exec(context.Background(), newRun(source)); err == nil {
			t.Errorf("%s ran: %+v", source, out)
		}
	}
	got := run(t, `pm.variables.set('out', [typeof process, typeof fetch, typeof XMLHttpRequest, typeof setTimeout].join(','))`)
	if got["out"] != "undefined,undefined,undefined,undefined" {
		t.Errorf("globals: %s", got["out"])
	}
}
