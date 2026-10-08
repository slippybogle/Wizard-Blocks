package stratum

import "strings"

// Version-rolling interpretations (BIP310 / BIP320).
//
// A miner that negotiated a mask via mining.configure sends a 6th
// mining.submit parameter, "version bits". Implementations disagree on how
// to apply it to the job version (verified against their sources):
//
//	bip310  version = (job & ~mask) | bits   BIP310 text
//	xor     version = job ^ bits             public-pool (MiningJob.ts);
//	                                         ESP-Miner (Bitaxe/NerdQaxe) submits rolled^job
//	or      version = job | bits             ckpool SV1 (stratifier.c "BIP320 mask OR");
//	                                         cgminer/bmminer (Antminer) submit the OR'd mask bits
//
// All three are identical when the job version has no bits inside the mask
// (the normal mainnet case: 0x20000000 & 0x1fffe000 == 0). They differ when a
// template signals a BIP9 deployment on a bit inside the mask (e.g. regtest
// "testdummy" on bit 28). Every interpretation keeps the bits outside the
// mask equal to the job's, and bits outside the negotiated mask are rejected
// up front, so each candidate is a legitimate header for the job: whichever
// one meets the target is a genuinely valid block.
const (
	InterpBIP310 = "bip310"
	InterpXOR    = "xor"
	InterpOR     = "or"
	InterpNone   = "none" // no version bits submitted: job version as-is
)

type versionCandidate struct {
	version uint32
	interp  string // one name, or several joined with "+" when they coincide
}

// versionCandidates returns the distinct header versions for submitted bits
// (which must already be checked to lie inside mask), in a fixed order.
func versionCandidates(job, mask, bits uint32) []versionCandidate {
	all := []versionCandidate{
		{job&^mask | bits&mask, InterpBIP310},
		{job ^ bits, InterpXOR},
		{job | bits, InterpOR},
	}
	var out []versionCandidate
	for _, c := range all {
		merged := false
		for i := range out {
			if out[i].version == c.version {
				out[i].interp += "+" + c.interp
				merged = true
			}
		}
		if !merged {
			out = append(out, c)
		}
	}
	return out
}

// HasInterp reports whether an interpretation label includes name.
func HasInterp(label, name string) bool {
	for _, p := range strings.Split(label, "+") {
		if p == name {
			return true
		}
	}
	return false
}
