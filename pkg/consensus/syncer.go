package consensus

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	requestTimeout = time.Minute
)

// syncer downloads blocks and block proposals, validates them
// and connect them to the chain.
//
// The synchronization steps:
// 1. got a new block hash
// 2. get the block B corresponding to the hash
// 3. get all prev block of the block, until connected to the chain,
// or reached the finalized block in the chain but can not connect to
// the chain, stop if can not connect to the chain
// 4. validate B and all it's prev blocks, then connect to the chain
// if valid
// 5. validate BP, then connect to the chain if validate
type syncer struct {
	v         *validator
	chain     *Chain
	requester requester
}

func newSyncer(v *validator, chain *Chain, requester requester) *syncer {
	return &syncer{
		v:         v,
		chain:     chain,
		requester: requester,
	}
}

type requester interface {
	RequestBlock(ctx context.Context, addr unicastAddr, hash Hash) (*Block, error)
	RequestBlockProposal(ctx context.Context, addr unicastAddr, hash Hash) (*BlockProposal, error)
	RequestRandBeaconSig(ctx context.Context, addr unicastAddr, round uint64) (*RandBeaconSig, error)
}

var errCanNotConnectToChain = errors.New("can not connect to chain")

func (s *syncer) SyncBlock(addr unicastAddr, hash Hash, round uint64) (b *Block, broadcast bool, err error) {
	b = s.chain.Block(hash)
	if b != nil {
		// already connected to the chain
		return
	}

	if round <= s.chain.FinalizedRound() {
		err = errCanNotConnectToChain
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	b, err = s.requester.RequestBlock(ctx, addr, hash)
	cancel()
	if err != nil {
		return
	}

	bp, _, err := s.SyncBlockProposal(addr, b.BlockProposal)
	if err != nil {
		return
	}

	var weight float64
	s.chain.RandomBeacon.WaitUntil(b.Round)
	prev := s.chain.Block(b.PrevBlock)
	if prev == nil {
		err = errors.New("impossible: prev block not found")
		return
	}

	if prev.Round != b.Round-1 {
		err = fmt.Errorf("invalid block, prev round: %d, cur round: %d", prev.Round, b.Round)
		return
	}

	_, _, nt := s.chain.RandomBeacon.Committees(b.Round)
	success := b.NotarizationSig.Verify(s.chain.RandomBeacon.groups[nt].PK, b.Encode(false))
	if !success {
		err = fmt.Errorf("validate block group sig failed, group:%d", nt)
		return
	}

	rank, err := s.chain.RandomBeacon.Rank(b.Owner, b.Round)
	if err != nil {
		err = fmt.Errorf("error get rank, but group sig is valid: %v", err)
		return
	}
	weight = rankToWeight(rank)

	state := s.chain.BlockToState(b.PrevBlock)
	trans, err := getTransition(state, bp.Data, bp.Round)
	if err != nil {
		return
	}

	if trans.StateHash() != b.StateRoot {
		err = errors.New("invalid state root")
		return
	}

	state = trans.Commit()
	_, err = s.chain.addBlock(b, bp, state, weight)
	if err != nil {
		return
	}

	return
}

func (s *syncer) SyncBlockProposal(addr unicastAddr, hash Hash) (bp *BlockProposal, broadcast bool, err error) {
	if bp = s.chain.BlockProposal(hash); bp != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	bp, err = s.requester.RequestBlockProposal(ctx, addr, hash)
	cancel()
	if err != nil {
		return
	}

	var prev *Block
	if bp.Round == 1 {
		if bp.PrevBlock != s.chain.Genesis() {
			err = errCanNotConnectToChain
			return
		}
		prev = s.chain.Block(s.chain.Genesis())
	} else {
		prev, _, err = s.SyncBlock(addr, bp.PrevBlock, bp.Round-1)
		if err != nil {
			return
		}
	}

	s.chain.RandomBeacon.WaitUntil(bp.Round)

	if prev.Round != bp.Round-1 {
		err = errors.New("prev block round is not block proposal round - 1")
		return
	}

	rank, err := s.chain.RandomBeacon.Rank(bp.Owner, bp.Round)
	if err != nil {
		return
	}

	pk, ok := s.chain.LastFinalizedSysState.addrToPK[bp.Owner]
	if !ok {
		err = errors.New("block proposal owner not found")
		return
	}

	if !bp.OwnerSig.Verify(pk, bp.Encode(false)) {
		err = errors.New("invalid block proposal signature")
		return
	}

	if bp.Round == s.chain.Round() {
		broadcast, err = s.chain.addBP(bp, rankToWeight(rank))
		if err != nil {
			panic(err)
		}
	}

	return
}

func (s *syncer) SyncRandBeaconSig(addr unicastAddr, round uint64) (bool, error) {
	if s.chain.RandomBeacon.Round() > round {
		return false, nil
	}

	var sigs []*RandBeaconSig
	for s.chain.RandomBeacon.Round() < round {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		sig, err := s.requester.RequestRandBeaconSig(ctx, addr, round)
		cancel()
		if err != nil {
			return false, err
		}
		sigs = append(sigs, sig)
		if sig.Round > 0 {
			round = sig.Round - 1
		} else {
			panic("syncing rand beacon sig of 0 round, should never happen")
		}
	}

	for i := len(sigs) - 1; i >= 0; i-- {
		sig := sigs[i]
		success := s.chain.RandomBeacon.AddRandBeaconSig(sig)
		if !success {
			return false, fmt.Errorf("failed to add rand beacon sig, round: %d, hash: %v", sig.Round, sig.Hash())
		}
	}

	return true, nil
}

/*

How does observer validate each block and update the state?

a. create token, send token, ICO:

  replay txns.

b. orders:

  replay each order txn to update the pending orders state, and then
  replay the trade receipts.

  observer does not need to do order matching, it can just replay the
  order matchin result according to the trade receipts.

  Order book: for the markets that the observer cares, he can
  reconstruct the order book of that market from the pending orders.

  Trade report: can be constructed from trade receipts.

steps:

  1. replay block proposal, but do not do order matching

  2. replay the trade receipts (order matching results)

  3. block proposals and trade receipts will be discarded after x
  blocks, we can have archiving nodes who persists them to disk or
  IPFS.

*/

/*

data structure related to state updates:

block:
  - state root hash
    state is a patricia merkle trie, it contains: token infos,
    accounts, pending orders.
  - receipt root hash
    receipt is a patricia merkle trie, it contains: trade receipts and
    token creation, send, freeze, burn receipts.

*/

/*

Stale client synchronization:

  a. download random beacon item from genesis to tip.

  b. download all key frames (contains group publications) from
  genesis to tip. The key frame is the first block of an epoch. L (a
  system parameter) consecutive blocks form an epoch. The genesis
  block is a key frame since it is the first block of the first
  epoch. Currently there is no open participation (groups are fixed),
  so only one key frame is necessary, L is set to infinity.

  c. download all the blocks, verify the block notarization. The block
  notarization is a threshold signature signed collected by a randomly
  selected group in each round. We can derive the group from the
  random beacon, and the group public key from the latest key frame.

  d. downloading the state of the (tip - n) block, replay the block
  proposal and trade receipts to tip, and verify that the state root
  hashes matches.

*/

/*

Do we need to shard block producers?

  Matching order should be way slower than collecting transactions:
  collecting transactions only involes transactions in the current
  block, while matching orders involves all past orders.

*/