package address

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

const b58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Base58CheckDecode decodes s and verifies its 4-byte SHA256d checksum.
func Base58CheckDecode(s string) ([]byte, error) {
	if len(s) == 0 {
		return nil, errors.New("empty base58 string")
	}
	n := new(big.Int)
	radix := big.NewInt(58)
	for _, c := range s {
		i := strings.IndexRune(b58Alphabet, c)
		if i < 0 {
			return nil, fmt.Errorf("invalid base58 character %q", c)
		}
		n.Mul(n, radix)
		n.Add(n, big.NewInt(int64(i)))
	}
	b := n.Bytes()
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	full := append(make([]byte, zeros), b...)
	if len(full) < 5 {
		return nil, errors.New("base58 data too short")
	}
	payload, sum := full[:len(full)-4], full[len(full)-4:]
	h1 := sha256.Sum256(payload)
	h2 := sha256.Sum256(h1[:])
	if !bytes.Equal(h2[:4], sum) {
		return nil, errors.New("base58 checksum mismatch")
	}
	return payload, nil
}

// Base58CheckEncode encodes payload with a checksum (used by tests/tools).
func Base58CheckEncode(payload []byte) string {
	h1 := sha256.Sum256(payload)
	h2 := sha256.Sum256(h1[:])
	full := append(append([]byte(nil), payload...), h2[:4]...)
	n := new(big.Int).SetBytes(full)
	radix := big.NewInt(58)
	mod := new(big.Int)
	var out []byte
	for n.Sign() > 0 {
		n.DivMod(n, radix, mod)
		out = append(out, b58Alphabet[mod.Int64()])
	}
	for _, c := range full {
		if c != 0 {
			break
		}
		out = append(out, '1')
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

// ---- bech32 / bech32m (BIP173, BIP350) ----

const bech32Charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

const (
	bech32Const  = 1
	bech32mConst = 0x2bc830a3
)

func bech32Polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		b := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (b>>i)&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

func bech32HRPExpand(hrp string) []byte {
	out := make([]byte, 0, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]>>5)
	}
	out = append(out, 0)
	for i := 0; i < len(hrp); i++ {
		out = append(out, hrp[i]&31)
	}
	return out
}

// bech32Decode returns hrp, 5-bit data (without checksum) and the checksum constant.
func bech32Decode(s string) (string, []byte, uint32, error) {
	if len(s) > 90 {
		return "", nil, 0, errors.New("bech32 string too long")
	}
	if strings.ToLower(s) != s && strings.ToUpper(s) != s {
		return "", nil, 0, errors.New("bech32 mixed case")
	}
	s = strings.ToLower(s)
	pos := strings.LastIndexByte(s, '1')
	if pos < 1 || pos+7 > len(s) {
		return "", nil, 0, errors.New("bech32 separator position invalid")
	}
	hrp := s[:pos]
	for i := 0; i < len(hrp); i++ {
		if hrp[i] < 33 || hrp[i] > 126 {
			return "", nil, 0, errors.New("bech32 hrp invalid character")
		}
	}
	data := make([]byte, 0, len(s)-pos-1)
	for i := pos + 1; i < len(s); i++ {
		d := strings.IndexByte(bech32Charset, s[i])
		if d < 0 {
			return "", nil, 0, fmt.Errorf("bech32 invalid character %q", s[i])
		}
		data = append(data, byte(d))
	}
	c := bech32Polymod(append(bech32HRPExpand(hrp), data...))
	if c != bech32Const && c != bech32mConst {
		return "", nil, 0, errors.New("bech32 checksum mismatch")
	}
	return hrp, data[:len(data)-6], c, nil
}

func bech32Encode(hrp string, data []byte, constant uint32) string {
	values := append(bech32HRPExpand(hrp), data...)
	poly := bech32Polymod(append(values, 0, 0, 0, 0, 0, 0)) ^ constant
	var sb strings.Builder
	sb.WriteString(hrp)
	sb.WriteByte('1')
	for _, d := range data {
		sb.WriteByte(bech32Charset[d])
	}
	for i := 0; i < 6; i++ {
		sb.WriteByte(bech32Charset[(poly>>(5*(5-i)))&31])
	}
	return sb.String()
}

// convertBits regroups bits; with pad=false it enforces BIP173's rules on
// leftover bits (≤ 4 and all zero).
func convertBits(data []byte, from, to uint, pad bool) ([]byte, error) {
	acc, bits := uint32(0), uint(0)
	maxv := uint32(1)<<to - 1
	var out []byte
	for _, v := range data {
		if uint32(v)>>from != 0 {
			return nil, errors.New("invalid data value")
		}
		acc = acc<<from | uint32(v)
		bits += from
		for bits >= to {
			bits -= to
			out = append(out, byte(acc>>bits&maxv))
		}
	}
	if pad {
		if bits > 0 {
			out = append(out, byte(acc<<(to-bits)&maxv))
		}
	} else if bits >= from || acc<<(to-bits)&maxv != 0 {
		return nil, errors.New("invalid padding")
	}
	return out, nil
}

// SegwitDecode decodes a segwit address (BIP173/BIP350 rules, including the
// bech32-for-v0 / bech32m-for-v1+ requirement).
func SegwitDecode(s string) (hrp string, version byte, program []byte, err error) {
	hrp, data, c, err := bech32Decode(s)
	if err != nil {
		return "", 0, nil, err
	}
	if len(data) < 1 {
		return "", 0, nil, errors.New("empty segwit data")
	}
	version = data[0]
	if version > 16 {
		return "", 0, nil, errors.New("invalid witness version")
	}
	program, err = convertBits(data[1:], 5, 8, false)
	if err != nil {
		return "", 0, nil, err
	}
	if len(program) < 2 || len(program) > 40 {
		return "", 0, nil, errors.New("invalid witness program length")
	}
	if version == 0 && len(program) != 20 && len(program) != 32 {
		return "", 0, nil, errors.New("invalid witness v0 program length")
	}
	if version == 0 && c != bech32Const {
		return "", 0, nil, errors.New("witness v0 must use bech32")
	}
	if version != 0 && c != bech32mConst {
		return "", 0, nil, errors.New("witness v1+ must use bech32m")
	}
	return hrp, version, program, nil
}

// SegwitEncode encodes a witness program (tests/tools).
func SegwitEncode(hrp string, version byte, program []byte) (string, error) {
	d, err := convertBits(program, 8, 5, true)
	if err != nil {
		return "", err
	}
	c := uint32(bech32Const)
	if version != 0 {
		c = bech32mConst
	}
	return bech32Encode(hrp, append([]byte{version}, d...), c), nil
}

// ---- CashAddr (BCH) ----

func cashPolymod(v []byte) uint64 {
	c := uint64(1)
	for _, d := range v {
		c0 := byte(c >> 35)
		c = ((c & 0x07ffffffff) << 5) ^ uint64(d)
		if c0&0x01 != 0 {
			c ^= 0x98f2bc8e61
		}
		if c0&0x02 != 0 {
			c ^= 0x79b76d99e2
		}
		if c0&0x04 != 0 {
			c ^= 0xf33e5fb3c4
		}
		if c0&0x08 != 0 {
			c ^= 0xae2eabe2a8
		}
		if c0&0x10 != 0 {
			c ^= 0x1e4f43e470
		}
	}
	return c ^ 1
}

func cashPrefixExpand(prefix string) []byte {
	out := make([]byte, 0, len(prefix)+1)
	for i := 0; i < len(prefix); i++ {
		out = append(out, prefix[i]&0x1f)
	}
	return append(out, 0)
}

var cashSizes = [8]int{20, 24, 28, 32, 40, 48, 56, 64}

// CashAddrDecode decodes a CashAddr string. If s has no prefix,
// defaultPrefix is assumed. Returns prefix, type (version bits 3..6) and hash.
func CashAddrDecode(s, defaultPrefix string) (prefix string, typ byte, hash []byte, err error) {
	if strings.ToLower(s) != s && strings.ToUpper(s) != s {
		return "", 0, nil, errors.New("cashaddr mixed case")
	}
	s = strings.ToLower(s)
	payloadStr := s
	prefix = defaultPrefix
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		prefix, payloadStr = s[:i], s[i+1:]
	}
	if prefix == "" {
		return "", 0, nil, errors.New("cashaddr missing prefix")
	}
	data := make([]byte, 0, len(payloadStr))
	for i := 0; i < len(payloadStr); i++ {
		d := strings.IndexByte(bech32Charset, payloadStr[i])
		if d < 0 {
			return "", 0, nil, fmt.Errorf("cashaddr invalid character %q", payloadStr[i])
		}
		data = append(data, byte(d))
	}
	if len(data) < 8+1 {
		return "", 0, nil, errors.New("cashaddr too short")
	}
	if cashPolymod(append(cashPrefixExpand(prefix), data...)) != 0 {
		return "", 0, nil, errors.New("cashaddr checksum mismatch")
	}
	payload, err := convertBits(data[:len(data)-8], 5, 8, false)
	if err != nil {
		return "", 0, nil, err
	}
	if len(payload) < 1 {
		return "", 0, nil, errors.New("cashaddr empty payload")
	}
	ver := payload[0]
	if ver&0x80 != 0 {
		return "", 0, nil, errors.New("cashaddr reserved version bit set")
	}
	hash = payload[1:]
	if len(hash) != cashSizes[ver&7] {
		return "", 0, nil, fmt.Errorf("cashaddr hash length %d does not match size bits", len(hash))
	}
	return prefix, (ver >> 3) & 0x0f, hash, nil
}

// CashAddrEncode encodes a CashAddr (tests/tools).
func CashAddrEncode(prefix string, typ byte, hash []byte) (string, error) {
	sizeBits := -1
	for i, n := range cashSizes {
		if n == len(hash) {
			sizeBits = i
		}
	}
	if sizeBits < 0 || typ > 15 {
		return "", errors.New("unsupported cashaddr type/size")
	}
	payload := append([]byte{typ<<3 | byte(sizeBits)}, hash...)
	d, err := convertBits(payload, 8, 5, true)
	if err != nil {
		return "", err
	}
	poly := cashPolymod(append(append(cashPrefixExpand(prefix), d...), 0, 0, 0, 0, 0, 0, 0, 0))
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteByte(':')
	for _, x := range d {
		sb.WriteByte(bech32Charset[x])
	}
	for i := 0; i < 8; i++ {
		sb.WriteByte(bech32Charset[(poly>>(5*(7-i)))&31])
	}
	return sb.String(), nil
}

// CashAddr types (CashAddr spec + CHIP-2022-02 CashTokens token-aware types).
const (
	cashP2PKH      = 0
	cashP2SH       = 1
	cashTokenP2PKH = 2
	cashTokenP2SH  = 3
)

func decodeCashAddr(n *Network, s string) (*Address, error) {
	prefix, typ, hash, err := CashAddrDecode(s, n.CashPrefix)
	if err != nil {
		return nil, err
	}
	if prefix != n.CashPrefix {
		return nil, fmt.Errorf("cashaddr prefix %q is not %q (%s)", prefix, n.CashPrefix, n.Chain)
	}
	switch {
	case (typ == cashP2PKH || typ == cashTokenP2PKH) && len(hash) == 20:
		return &Address{String: s, Type: "p2pkh", Script: p2pkh(hash)}, nil
	case (typ == cashP2SH || typ == cashTokenP2SH) && len(hash) == 20:
		return &Address{String: s, Type: "p2sh", Script: p2sh(hash)}, nil
	case (typ == cashP2SH || typ == cashTokenP2SH) && len(hash) == 32:
		return &Address{String: s, Type: "p2sh32", Script: p2sh32(hash)}, nil
	}
	return nil, fmt.Errorf("unsupported cashaddr type %d with %d-byte hash", typ, len(hash))
}
