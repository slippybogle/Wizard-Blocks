// Package address decodes payout addresses into scriptPubKeys for BTC
// (Base58Check P2PKH/P2SH, BIP173 bech32 segwit v0, BIP350 bech32m taproot)
// and BCH (CashAddr incl. P2SH32 and token-aware types, legacy Base58Check).
//
// The engine never trusts this decoding alone: at startup and on every
// authorize it is cross-checked against the node's validateaddress
// scriptPubKey. Two independent implementations must agree before any block
// reward is directed to a script.
package address

import (
	"errors"
	"fmt"
	"strings"
)

// Coin identifies the chain family.
type Coin string

const (
	BTC Coin = "btc"
	BCH Coin = "bch"
)

// Network holds address encoding parameters for one coin + chain.
type Network struct {
	Coin       Coin
	Chain      string // as reported by getblockchaininfo
	PubKeyHash byte
	ScriptHash byte
	Bech32HRP  string // BTC only
	CashPrefix string // BCH only
}

// NetworkFor maps a coin and a getblockchaininfo "chain" value to address
// parameters.
func NetworkFor(coin Coin, chain string) (*Network, error) {
	n := &Network{Coin: coin, Chain: chain}
	switch coin {
	case BTC:
		switch chain {
		case "main":
			n.PubKeyHash, n.ScriptHash, n.Bech32HRP = 0x00, 0x05, "bc"
		case "test", "testnet4", "signet":
			n.PubKeyHash, n.ScriptHash, n.Bech32HRP = 0x6f, 0xc4, "tb"
		case "regtest":
			n.PubKeyHash, n.ScriptHash, n.Bech32HRP = 0x6f, 0xc4, "bcrt"
		default:
			return nil, fmt.Errorf("unknown BTC chain %q", chain)
		}
	case BCH:
		switch chain {
		case "main":
			n.PubKeyHash, n.ScriptHash, n.CashPrefix = 0x00, 0x05, "bitcoincash"
		case "test", "test4", "testnet4", "scale", "chipnet":
			n.PubKeyHash, n.ScriptHash, n.CashPrefix = 0x6f, 0xc4, "bchtest"
		case "regtest":
			n.PubKeyHash, n.ScriptHash, n.CashPrefix = 0x6f, 0xc4, "bchreg"
		default:
			return nil, fmt.Errorf("unknown BCH chain %q", chain)
		}
	default:
		return nil, fmt.Errorf("unknown coin %q", coin)
	}
	return n, nil
}

// Address is a decoded payout destination.
type Address struct {
	String string // as supplied
	Type   string // p2pkh, p2sh, p2sh32, p2wpkh, p2wsh, p2tr
	Script []byte // scriptPubKey
}

// Decode decodes s for network n and returns its scriptPubKey. Only script
// types that are unambiguously spendable by their owner are accepted:
// witness versions ≥ 2 and non-standard program sizes are refused because a
// block reward sent there could be unspendable or anyone-can-spend.
func Decode(n *Network, s string) (*Address, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("empty address")
	}
	if len(s) > 120 {
		return nil, errors.New("address too long")
	}
	switch n.Coin {
	case BTC:
		lower := strings.ToLower(s)
		for _, hrp := range []string{n.Bech32HRP, "bc", "tb", "bcrt"} {
			if strings.HasPrefix(lower, hrp+"1") {
				return decodeSegwit(n, s) // reports a wrong-network prefix clearly
			}
		}
		return decodeBase58(n, s)
	case BCH:
		if a, err := decodeCashAddr(n, s); err == nil {
			return a, nil
		} else if strings.Contains(s, ":") || !looksBase58(s) {
			return nil, err
		}
		return decodeBase58(n, s)
	}
	return nil, fmt.Errorf("unknown coin %q", n.Coin)
}

func looksBase58(s string) bool {
	for _, c := range s {
		if strings.IndexRune(b58Alphabet, c) < 0 {
			return false
		}
	}
	return true
}

func p2pkh(h []byte) []byte {
	s := []byte{0x76, 0xa9, 0x14}
	s = append(s, h...)
	return append(s, 0x88, 0xac)
}

func p2sh(h []byte) []byte {
	s := []byte{0xa9, 0x14}
	s = append(s, h...)
	return append(s, 0x87)
}

// p2sh32 is the BCH 2023-05 (CHIP-2022-05) 32-byte script hash form:
// OP_HASH256 <32 bytes> OP_EQUAL.
func p2sh32(h []byte) []byte {
	s := []byte{0xaa, 0x20}
	s = append(s, h...)
	return append(s, 0x87)
}

func decodeBase58(n *Network, s string) (*Address, error) {
	payload, err := Base58CheckDecode(s)
	if err != nil {
		return nil, err
	}
	if len(payload) < 1 {
		return nil, errors.New("empty base58 payload")
	}
	ver, h := payload[0], payload[1:]
	switch {
	case ver == n.PubKeyHash && len(h) == 20:
		return &Address{String: s, Type: "p2pkh", Script: p2pkh(h)}, nil
	case ver == n.ScriptHash && len(h) == 20:
		return &Address{String: s, Type: "p2sh", Script: p2sh(h)}, nil
	case n.Coin == BCH && ver == n.ScriptHash && len(h) == 32:
		return &Address{String: s, Type: "p2sh32", Script: p2sh32(h)}, nil
	}
	if ver != n.PubKeyHash && ver != n.ScriptHash {
		return nil, fmt.Errorf("base58 version byte 0x%02x is not valid for %s %s", ver, n.Coin, n.Chain)
	}
	return nil, fmt.Errorf("base58 payload length %d invalid", len(h))
}

func decodeSegwit(n *Network, s string) (*Address, error) {
	hrp, ver, prog, err := SegwitDecode(s)
	if err != nil {
		return nil, err
	}
	if hrp != n.Bech32HRP {
		return nil, fmt.Errorf("address prefix %q is not %q (%s)", hrp, n.Bech32HRP, n.Chain)
	}
	script := make([]byte, 0, 2+len(prog))
	switch {
	case ver == 0 && len(prog) == 20:
		script = append(script, 0x00, 20)
		return &Address{String: s, Type: "p2wpkh", Script: append(script, prog...)}, nil
	case ver == 0 && len(prog) == 32:
		script = append(script, 0x00, 32)
		return &Address{String: s, Type: "p2wsh", Script: append(script, prog...)}, nil
	case ver == 1 && len(prog) == 32:
		script = append(script, 0x51, 32)
		return &Address{String: s, Type: "p2tr", Script: append(script, prog...)}, nil
	case ver == 0:
		return nil, fmt.Errorf("invalid witness v0 program length %d", len(prog))
	default:
		return nil, fmt.Errorf("refusing witness version %d program length %d as payout (not a defined output type)", ver, len(prog))
	}
}
