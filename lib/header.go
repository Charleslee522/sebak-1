package sebak

import (
	"time"

	"boscoin.io/sebak/lib/common"
)

type Header struct {
	Version             uint32
	PrevBlockHash       string // [TODO] Uint256 type
	TransactionsRoot    string // Merkle root of Txs [TODO] Uint256 type
	Timestamp           time.Time
	Height              uint64
	TotalTxs            uint64
	PrevConsensusResult ConsensusResult

	BlockHash               string // [TODO] Uint256 type
	prevTotalTxs            uint64
	prevConsensusResultHash string // [TODO] Uint256 type
	// ConsensusPayloadHash    Uint256
	// ConsensusPayload        Payload  // or []byte
	// StateRoot types.Hash    // MPT of state
	// [TODO] + smart contract fields
}

func NewHeader(height uint64, prevBlockHash string, prevResult ConsensusResult, prevTotalTxs uint64, currentTxs uint64, txRoot string) *Header {
	p := Header{
		PrevBlockHash:       prevBlockHash,
		Timestamp:           time.Now(),
		Height:              height,
		PrevConsensusResult: prevResult,
		TotalTxs:            prevTotalTxs + currentTxs,
		TransactionsRoot:    txRoot,
	}
	p.fill()

	return &p
}

func (h *Header) fill() {
	if h.Version == 0 {
		// [TODO] fill Version
	}

	if h.PrevBlockHash == "" {
		if h.Height != 0 &&
			h.PrevConsensusResult.BlockHash != "" {
			h.PrevBlockHash = h.PrevConsensusResult.BlockHash
		}
	}

	if h.BlockHash == "" {
		h.BlockHash = sebakcommon.MustMakeObjectHashString(h)
	}
}

type ConsensusResult struct {
	BlockHash string // [TODO] Uint256 type
	Ballots   []*Ballot
}