package script

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	_ "crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/buffer"

	"http_gui/internal/vars"
)

// Node's crypto.constants, of which these are used.
const (
	rsaPKCS1Padding    = 1
	rsaOAEPPadding     = 4
	rsaPSSPadding      = 6
	rsaPSSSaltLenHash  = -1
	rsaPSSSaltLenMax   = -2
	dsaEncodingP1363   = "ieee-p1363"
	defaultOAEPHash    = "sha1"
	errNeedsPrivateKey = "a private key is needed here, and this is a public key"
)

// nodeCrypto is require('crypto'): the part of Node's module that request
// signing uses, on Go's crypto packages.
type nodeCrypto struct {
	vm *goja.Runtime
}

// loadNodeCrypto is the loader of the "crypto" module.
func loadNodeCrypto(vm *goja.Runtime, module *goja.Object) {
	c := &nodeCrypto{vm: vm}
	exports := module.Get("exports").(*goja.Object)
	exports.Set("createHash", c.createHash)
	exports.Set("createHmac", c.createHmac)
	exports.Set("createSign", c.createSign)
	exports.Set("createVerify", c.createVerify)
	exports.Set("sign", c.sign)
	exports.Set("verify", c.verify)
	exports.Set("publicEncrypt", c.publicEncrypt)
	exports.Set("privateDecrypt", c.privateDecrypt)
	exports.Set("randomBytes", c.randomBytes)
	exports.Set("randomUUID", func() string { return vars.UUID() })
	exports.Set("constants", map[string]int{
		"RSA_PKCS1_PADDING":        rsaPKCS1Padding,
		"RSA_PKCS1_OAEP_PADDING":   rsaOAEPPadding,
		"RSA_PKCS1_PSS_PADDING":    rsaPSSPadding,
		"RSA_PSS_SALTLEN_DIGEST":   rsaPSSSaltLenHash,
		"RSA_PSS_SALTLEN_MAX_SIGN": rsaPSSSaltLenMax,
		"RSA_PSS_SALTLEN_AUTO":     rsaPSSSaltLenMax,
	})
}

// throw raises a JavaScript Error with this message.
func (c *nodeCrypto) throw(format string, args ...any) {
	e, err := c.vm.New(c.vm.Get("Error"), c.vm.ToValue(fmt.Sprintf(format, args...)))
	if err != nil {
		panic(c.vm.NewGoError(fmt.Errorf(format, args...)))
	}
	panic(e)
}

// bytes returns the bytes of a string, in the encoding given (UTF-8 by
// default), or of a Buffer or typed array.
func (c *nodeCrypto) bytes(data, enc goja.Value) []byte {
	return buffer.DecodeBytes(c.vm, data, enc)
}

// hash returns the hash of a digest name, as Node writes them: sha256,
// SHA-256, RSA-SHA256, sha256WithRSAEncryption.
func (c *nodeCrypto) hash(name string) crypto.Hash {
	n := strings.ReplaceAll(strings.ToLower(name), "-", "")
	n = strings.TrimPrefix(n, "rsa")
	n = strings.TrimPrefix(n, "ecdsawith")
	n = strings.TrimSuffix(n, "withrsaencryption")
	switch n {
	case "md5":
		return crypto.MD5
	case "sha1":
		return crypto.SHA1
	case "sha256":
		return crypto.SHA256
	case "sha384":
		return crypto.SHA384
	case "sha512":
		return crypto.SHA512
	}
	c.throw("unknown digest %q: use md5, sha1, sha256, sha384 or sha512", name)
	return 0
}

// hasher makes the object createHash and createHmac return.
func (c *nodeCrypto) hasher(h hash.Hash) goja.Value {
	o := c.vm.NewObject()
	o.Set("update", func(call goja.FunctionCall) goja.Value {
		h.Write(c.bytes(call.Argument(0), call.Argument(1)))
		return o
	})
	o.Set("digest", func(call goja.FunctionCall) goja.Value {
		return buffer.EncodeBytes(c.vm, h.Sum(nil), call.Argument(0))
	})
	return o
}

func (c *nodeCrypto) createHash(call goja.FunctionCall) goja.Value {
	return c.hasher(c.hash(call.Argument(0).String()).New())
}

func (c *nodeCrypto) createHmac(call goja.FunctionCall) goja.Value {
	h := c.hash(call.Argument(0).String())
	return c.hasher(hmac.New(h.New, c.bytes(call.Argument(1), goja.Undefined())))
}

func (c *nodeCrypto) createSign(call goja.FunctionCall) goja.Value {
	alg := call.Argument(0).String()
	var data bytes.Buffer
	o := c.vm.NewObject()
	o.Set("update", func(call goja.FunctionCall) goja.Value {
		data.Write(c.bytes(call.Argument(0), call.Argument(1)))
		return o
	})
	o.Set("sign", func(call goja.FunctionCall) goja.Value {
		return buffer.EncodeBytes(c.vm, c.signData(alg, data.Bytes(), call.Argument(0)), call.Argument(1))
	})
	return o
}

func (c *nodeCrypto) createVerify(call goja.FunctionCall) goja.Value {
	alg := call.Argument(0).String()
	var data bytes.Buffer
	o := c.vm.NewObject()
	o.Set("update", func(call goja.FunctionCall) goja.Value {
		data.Write(c.bytes(call.Argument(0), call.Argument(1)))
		return o
	})
	o.Set("verify", func(call goja.FunctionCall) goja.Value {
		sig := c.bytes(call.Argument(1), call.Argument(2))
		return c.vm.ToValue(c.verifyData(alg, data.Bytes(), call.Argument(0), sig))
	})
	return o
}

// sign is crypto.sign(algorithm, data, key): algorithm is null for Ed25519.
func (c *nodeCrypto) sign(call goja.FunctionCall) goja.Value {
	data := c.bytes(call.Argument(1), goja.Undefined())
	return buffer.WrapBytes(c.vm, c.signData(algorithm(call.Argument(0)), data, call.Argument(2)))
}

// verify is crypto.verify(algorithm, data, key, signature).
func (c *nodeCrypto) verify(call goja.FunctionCall) goja.Value {
	data := c.bytes(call.Argument(1), goja.Undefined())
	sig := c.bytes(call.Argument(3), goja.Undefined())
	return c.vm.ToValue(c.verifyData(algorithm(call.Argument(0)), data, call.Argument(2), sig))
}

// algorithm returns a digest name, "" for null or undefined.
func algorithm(v goja.Value) string {
	if goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

func (c *nodeCrypto) signData(alg string, data []byte, keyArg goja.Value) []byte {
	key, opts := c.key(keyArg)
	var sig []byte
	var err error
	switch k := key.(type) {
	case ed25519.PrivateKey:
		if alg != "" {
			c.throw("Ed25519 signs the data itself: use crypto.sign(null, data, key)")
		}
		sig = ed25519.Sign(k, data)
	case *rsa.PrivateKey:
		h, digest := c.digest(alg, data)
		if opts.int("padding", rsaPKCS1Padding) == rsaPSSPadding {
			sig, err = rsa.SignPSS(rand.Reader, k, h, digest, opts.pss(h))
		} else {
			sig, err = rsa.SignPKCS1v15(rand.Reader, k, h, digest)
		}
	case *ecdsa.PrivateKey:
		_, digest := c.digest(alg, data)
		if opts.string("dsaEncoding", "der") != dsaEncodingP1363 {
			sig, err = ecdsa.SignASN1(rand.Reader, k, digest)
			break
		}
		// r and s, each as wide as the curve, as JWTs carry them.
		var r, s *big.Int
		if r, s, err = ecdsa.Sign(rand.Reader, k, digest); err == nil {
			n := (k.Curve.Params().BitSize + 7) / 8
			sig = make([]byte, 2*n)
			r.FillBytes(sig[:n])
			s.FillBytes(sig[n:])
		}
	default:
		c.throw(errNeedsPrivateKey)
	}
	if err != nil {
		c.throw("signing: %v", err)
	}
	return sig
}

func (c *nodeCrypto) verifyData(alg string, data []byte, keyArg goja.Value, sig []byte) bool {
	key, opts := c.key(keyArg)
	if signer, ok := key.(crypto.Signer); ok {
		key = signer.Public()
	}
	switch k := key.(type) {
	case ed25519.PublicKey:
		if alg != "" {
			c.throw("Ed25519 verifies the data itself: use crypto.verify(null, data, key, signature)")
		}
		return ed25519.Verify(k, data, sig)
	case *rsa.PublicKey:
		h, digest := c.digest(alg, data)
		if opts.int("padding", rsaPKCS1Padding) == rsaPSSPadding {
			return rsa.VerifyPSS(k, h, digest, sig, opts.pss(h)) == nil
		}
		return rsa.VerifyPKCS1v15(k, h, digest, sig) == nil
	case *ecdsa.PublicKey:
		_, digest := c.digest(alg, data)
		if opts.string("dsaEncoding", "der") != dsaEncodingP1363 {
			return ecdsa.VerifyASN1(k, digest, sig)
		}
		n := len(sig) / 2
		return len(sig)%2 == 0 && ecdsa.Verify(k, digest, new(big.Int).SetBytes(sig[:n]), new(big.Int).SetBytes(sig[n:]))
	}
	c.throw("unsupported key type %T", key)
	return false
}

// digest hashes data with the digest named.
func (c *nodeCrypto) digest(alg string, data []byte) (crypto.Hash, []byte) {
	if alg == "" {
		c.throw("this key needs a digest, as 'sha256'")
	}
	h := c.hash(alg)
	w := h.New()
	w.Write(data)
	return h, w.Sum(nil)
}

func (c *nodeCrypto) publicEncrypt(call goja.FunctionCall) goja.Value {
	key, opts := c.key(call.Argument(0))
	if signer, ok := key.(crypto.Signer); ok {
		key = signer.Public()
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		c.throw("publicEncrypt needs an RSA key")
	}
	data := c.bytes(call.Argument(1), goja.Undefined())
	var out []byte
	var err error
	if opts.int("padding", rsaOAEPPadding) == rsaPKCS1Padding {
		out, err = rsa.EncryptPKCS1v15(rand.Reader, pub, data)
	} else {
		h := c.hash(opts.string("oaepHash", defaultOAEPHash))
		out, err = rsa.EncryptOAEP(h.New(), rand.Reader, pub, data, nil)
	}
	if err != nil {
		c.throw("encrypting: %v", err)
	}
	return buffer.WrapBytes(c.vm, out)
}

func (c *nodeCrypto) privateDecrypt(call goja.FunctionCall) goja.Value {
	key, opts := c.key(call.Argument(0))
	priv, ok := key.(*rsa.PrivateKey)
	if !ok {
		c.throw("privateDecrypt needs an RSA private key")
	}
	data := c.bytes(call.Argument(1), goja.Undefined())
	var out []byte
	var err error
	if opts.int("padding", rsaOAEPPadding) == rsaPKCS1Padding {
		out, err = rsa.DecryptPKCS1v15(rand.Reader, priv, data)
	} else {
		h := c.hash(opts.string("oaepHash", defaultOAEPHash))
		out, err = rsa.DecryptOAEP(h.New(), rand.Reader, priv, data, nil)
	}
	if err != nil {
		c.throw("decrypting: %v", err)
	}
	return buffer.WrapBytes(c.vm, out)
}

func (c *nodeCrypto) randomBytes(call goja.FunctionCall) goja.Value {
	n := call.Argument(0).ToInteger()
	if n < 0 || n > 1<<20 {
		c.throw("randomBytes: the size %d is out of range", n)
	}
	b := make([]byte, n)
	rand.Read(b) // never fails
	return buffer.WrapBytes(c.vm, b)
}

// keyOptions is the object a key is given in, as {key, padding}, nil for
// a key given alone.
type keyOptions struct {
	o *goja.Object
}

func (k keyOptions) get(name string) goja.Value {
	if k.o == nil {
		return nil
	}
	if v := k.o.Get(name); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		return v
	}
	return nil
}

func (k keyOptions) int(name string, fallback int) int {
	if v := k.get(name); v != nil {
		return int(v.ToInteger())
	}
	return fallback
}

func (k keyOptions) string(name, fallback string) string {
	if v := k.get(name); v != nil {
		return v.String()
	}
	return fallback
}

// pss returns the PSS options of the key: Node's saltLength, whose default
// is the longest that fits when signing and any when verifying.
func (k keyOptions) pss(h crypto.Hash) *rsa.PSSOptions {
	salt := k.int("saltLength", rsaPSSSaltLenMax)
	switch salt {
	case rsaPSSSaltLenMax:
		salt = rsa.PSSSaltLengthAuto
	case rsaPSSSaltLenHash:
		salt = rsa.PSSSaltLengthEqualsHash
	}
	return &rsa.PSSOptions{SaltLength: salt, Hash: h}
}

// key parses a key: PEM in a string or a Buffer, or an object holding one
// as its key, with options beside it. It returns a private key of
// crypto/rsa, crypto/ecdsa or crypto/ed25519, or a public one.
func (c *nodeCrypto) key(v goja.Value) (any, keyOptions) {
	var opts keyOptions
	if o, ok := v.(*goja.Object); ok {
		if inner := o.Get("key"); inner != nil && !goja.IsUndefined(inner) {
			opts.o, v = o, inner
		}
	}
	if goja.IsUndefined(v) || goja.IsNull(v) {
		c.throw("the key is missing")
	}
	key, err := parsePEMKey(c.bytes(v, goja.Undefined()))
	if err != nil {
		c.throw("%v", err)
	}
	return key, opts
}

func parsePEMKey(data []byte) (any, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("the key is not PEM: it should start with -----BEGIN")
	}
	if block.Type == "ENCRYPTED PRIVATE KEY" || block.Headers["Proc-Type"] != "" {
		return nil, errors.New("keys encrypted with a passphrase are not supported")
	}
	var key any
	var err error
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "PUBLIC KEY":
		key, err = x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		key, err = x509.ParsePKCS1PublicKey(block.Bytes)
	case "CERTIFICATE":
		var cert *x509.Certificate
		if cert, err = x509.ParseCertificate(block.Bytes); err == nil {
			key = cert.PublicKey
		}
	default:
		return nil, fmt.Errorf("unsupported PEM block %q", block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("parsing the key: %w", err)
	}
	return key, nil
}
