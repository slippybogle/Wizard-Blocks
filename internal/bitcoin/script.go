package bitcoin

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Script opcodes used by the engine.
const (
	Op0           = 0x00
	OpPushData1   = 0x4c
	OpPushData2   = 0x4d
	Op1Negate     = 0x4f
	Op1           = 0x51
	Op16          = 0x60
	OpReturn      = 0x6a
	OpDup         = 0x76
	OpEqual       = 0x87
	OpEqualVerify = 0x88
	OpHash160     = 0xa9
	OpHash256     = 0xaa
	OpCheckSig    = 0xac
)

// ScriptNum serializes n as a minimally-encoded CScriptNum (little-endian,
// sign bit in the top bit of the last byte).
func ScriptNum(n int64) []byte {
	if n == 0 {
		return nil
	}
	neg := n < 0
	abs := uint64(n)
	if neg {
		abs = uint64(-n)
	}
	var out []byte
	for abs > 0 {
		out = append(out, byte(abs&0xff))
		abs >>= 8
	}
	if out[len(out)-1]&0x80 != 0 {
		if neg {
			out = append(out, 0x80)
		} else {
			out = append(out, 0x00)
		}
	} else if neg {
		out[len(out)-1] |= 0x80
	}
	return out
}

// AppendPush appends a minimal data push of data (not for small integers;
// use AppendScriptInt for those).
func AppendPush(s []byte, data []byte) []byte {
	n := len(data)
	switch {
	case n < OpPushData1:
		s = append(s, byte(n))
	case n <= 0xff:
		s = append(s, OpPushData1, byte(n))
	default:
		s = append(s, OpPushData2)
		s = binary.LittleEndian.AppendUint16(s, uint16(n))
	}
	return append(s, data...)
}

// AppendScriptInt appends n exactly as Bitcoin Core's `CScript() << n`
// (CScript::push_int64): OP_0 for 0, OP_1NEGATE for -1, OP_1..OP_16 for
// 1..16, otherwise a push of the minimal CScriptNum.
func AppendScriptInt(s []byte, n int64) []byte {
	switch {
	case n == 0:
		return append(s, Op0)
	case n == -1:
		return append(s, Op1Negate)
	case n >= 1 && n <= 16:
		return append(s, byte(Op1+n-1))
	default:
		return AppendPush(s, ScriptNum(n))
	}
}

// BIP34HeightScript returns the scriptSig prefix required by BIP34: the block
// height serialized as `CScript() << height`. Both Bitcoin Core and BCHN
// verify the coinbase scriptSig *begins with* exactly these bytes.
func BIP34HeightScript(height int64) []byte {
	return AppendScriptInt(nil, height)
}

// DecodeBIP34Height decodes the height from the start of a coinbase scriptSig.
func DecodeBIP34Height(scriptSig []byte) (int64, error) {
	if len(scriptSig) == 0 {
		return 0, errors.New("empty scriptSig")
	}
	op := scriptSig[0]
	switch {
	case op == Op0:
		return 0, nil
	case op >= Op1 && op <= Op16:
		return int64(op-Op1) + 1, nil
	case op >= 1 && op <= 8:
		if len(scriptSig) < 1+int(op) {
			return 0, ErrShort
		}
		b := scriptSig[1 : 1+int(op)]
		var v int64
		for i := len(b) - 1; i >= 0; i-- {
			v = v<<8 | int64(b[i])
		}
		if b[len(b)-1]&0x80 != 0 {
			return 0, errors.New("negative height")
		}
		return v, nil
	default:
		return 0, fmt.Errorf("unexpected opcode 0x%02x for BIP34 height", op)
	}
}
