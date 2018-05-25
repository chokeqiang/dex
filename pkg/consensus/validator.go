package consensus

import (
	log "github.com/helinwang/log15"
)

// validator validates the data received from peers.
type validator struct {
	chain *Chain
}

func newValidator(chain *Chain) *validator {
	return &validator{chain: chain}
}

func (v *validator) ValidateBlock(b *Block) (float64, bool) {
	// TODO: validate sig, validate txns, validate sig
	return 0, true
}

func (v *validator) ValidateBlockProposal(bp *BlockProposal) (float64, bool) {
	// TODO: validate sig, validate txns, validate owner, validate
	// round is correct
	round := v.chain.RandomBeacon.Depth() - 1
	if bp.Round != round {
		if bp.Round > round {
			log.Warn("received block proposal of higher round", "round", bp.Round, "my round", round)
		}

		return 0, false
	}
	return 0, true
}

func (v *validator) ValidateNtShare(n *NtShare) (int, bool) {
	round := v.chain.Round()
	if n.Round != round {
		if n.Round > round {
			log.Warn("received nt share of higher round", "round", n.Round, "my round", round)
		}
		return 0, false
	}

	_, _, nt := v.chain.RandomBeacon.Committees(round)
	// TODO: validate sig, validate owner, validate round is
	// correct, validate share is signed correctly.
	return nt, true
}

func (v *validator) ValidateRandBeaconSig(r *RandBeaconSig) bool {
	// TODO: validate sig, owner, round, share
	round := v.chain.RandomBeacon.Depth()
	if r.Round != round {
		if r.Round > round {
			log.Warn("received RandBeaconSig of higher round", "round", r.Round, "my round", round)
		}
		return false
	}

	return true
}

func (v *validator) ValidateRandBeaconSigShare(r *RandBeaconSigShare) (int, bool) {
	round := v.chain.RandomBeacon.Depth()
	if r.Round != round {
		if r.Round > round {
			log.Warn("received RandBeaconSigShare of higher round", "round", r.Round, "my round", round)
		}
		return 0, false
	}

	rb, _, _ := v.chain.RandomBeacon.Committees(round)
	// TODO: validate sig, owner, round
	return rb, true
}
