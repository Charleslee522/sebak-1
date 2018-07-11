package sebakconsensus

import (
	"boscoin.io/sebak/lib"
	"boscoin.io/sebak/lib/common"
	"boscoin.io/sebak/lib/error"
	"boscoin.io/sebak/lib/node"
)

type IsaacBA struct {
	sebakcommon.SafeLock

	networkID             []byte
	Node                  *sebaknode.LocalNode
	VotingThresholdPolicy sebakcommon.VotingThresholdPolicy

	Boxes *sebak.BallotBoxes
}

func NewIsaacBA(networkID []byte, node *sebaknode.LocalNode, votingThresholdPolicy sebakcommon.VotingThresholdPolicy) (is *IsaacBA, err error) {
	is = &IsaacBA{
		networkID: networkID,
		Node:      node,
		VotingThresholdPolicy: votingThresholdPolicy,
		Boxes: sebak.NewBallotBoxes(),
	}

	return
}

func (is *IsaacBA) NetworkID() []byte {
	return is.networkID
}

func (is *IsaacBA) GetNode() *sebaknode.LocalNode {
	return is.Node
}

func (is *IsaacBA) HasMessage(message sebakcommon.Message) bool {
	return is.Boxes.HasMessage(message)
}

func (is *IsaacBA) HasMessageByHash(h string) bool {
	return is.Boxes.HasMessageByHash(h)
}

func (is *IsaacBA) ReceiveMessage(m sebakcommon.Message) (ballot sebak.Ballot, err error) {
	/*
		Previously the new incoming Message must be checked,
			- TODO `Message` must be saved in `BlockTransactionHistory`
			- TODO check already in BlockTransaction
			- TODO check already in BlockTransactionHistory
	*/

	if is.Boxes.HasMessage(m) {
		err = sebakerror.ErrorNewButKnownMessage
		return
	}

	if ballot, err = sebak.NewBallotFromMessage(is.Node.Address(), m); err != nil {
		return
	}

	// self-sign; make new `Ballot` from `Message`
	ballot.SetState(sebakcommon.BallotStateINIT)
	ballot.Vote(sebak.VotingYES) // The initial ballot from client will have 'VotingYES'
	ballot.Sign(is.Node.Keypair(), is.networkID)

	if err = ballot.IsWellFormed(is.networkID); err != nil {
		return
	}

	if _, err = is.Boxes.AddBallot(ballot); err != nil {
		return
	}

	return
}

func (is *IsaacBA) ReceiveBallot(ballot sebak.Ballot) (vs sebak.VotingStateStaging, err error) {
	switch ballot.State() {
	case sebakcommon.BallotStateINIT:
		vs, err = is.receiveBallotStateINIT(ballot)
	case sebakcommon.BallotStateALLCONFIRM:
		err = sebakerror.ErrorBallotHasInvalidState
	default:
		vs, err = is.receiveBallotVotingStates(ballot)
	}

	return
}

func (is *IsaacBA) receiveBallotStateINIT(ballot sebak.Ballot) (vs sebak.VotingStateStaging, err error) {
	var isNew bool

	if isNew, err = is.Boxes.AddBallot(ballot); err != nil {
		return
	}

	if isNew {
		var newBallot sebak.Ballot
		newBallot, err = sebak.NewBallotFromMessage(is.Node.Keypair().Address(), ballot.Data().Message())
		if err != nil {
			return
		}

		// self-sign
		newBallot.SetState(sebakcommon.BallotStateINIT)
		newBallot.Vote(sebak.VotingYES) // The BallotStateINIT ballot will have 'VotingYES'
		newBallot.Sign(is.Node.Keypair(), is.networkID)

		if err = newBallot.IsWellFormed(is.networkID); err != nil {
			return
		}

		if _, err = is.Boxes.AddBallot(newBallot); err != nil {
			return
		}
	}

	vr, err := is.Boxes.VotingResult(ballot)
	if err != nil {
		return
	}

	if vr.IsClosed() || !vr.CanGetResult(is.VotingThresholdPolicy) {
		return
	}

	votingHole, state, ended := vr.MakeResult(is.VotingThresholdPolicy)
	if ended {
		if vs, err = vr.ChangeState(votingHole, state); err != nil {
			return
		}

		is.Boxes.WaitingBox.RemoveVotingResult(vr) // TODO detect error
		if !vs.IsClosed() {
			is.Boxes.VotingBox.AddVotingResult(vr) // TODO detect error
			is.Boxes.AddSource(ballot)
		}
	}

	return
}

// AddBallot
//
// NOTE(ISSAC.AddBallot): `ISSAC.AddBallot()` only for self-signed Ballot
func (is *IsaacBA) AddBallot(ballot sebak.Ballot) (err error) {
	vr, err := is.Boxes.VotingResult(ballot)
	if err != nil {
		return
	}
	if vr.IsVoted(ballot) {
		return nil
	}
	_, err = is.Boxes.AddBallot(ballot)
	return
}

func (is *IsaacBA) CloseConsensus(ballot sebak.Ballot) (err error) {
	if !is.HasMessageByHash(ballot.MessageHash()) {
		return sebakerror.ErrorVotingResultNotInBox
	}

	vr, err := is.Boxes.VotingResult(ballot)
	if err != nil {
		return
	}

	is.Boxes.WaitingBox.RemoveVotingResult(vr)  // TODO detect error
	is.Boxes.VotingBox.RemoveVotingResult(vr)   // TODO detect error
	is.Boxes.ReservedBox.RemoveVotingResult(vr) // TODO detect error
	is.Boxes.RemoveVotingResult(vr)             // TODO detect error

	return
}

func (is *IsaacBA) receiveBallotVotingStates(ballot sebak.Ballot) (vs sebak.VotingStateStaging, err error) {
	if _, err = is.Boxes.AddBallot(ballot); err != nil {
		return
	}

	if !is.Boxes.VotingBox.HasMessageByHash(ballot.MessageHash()) {
		is.Boxes.AddSource(ballot)
	}

	var vr *sebak.VotingResult

	if vr, err = is.Boxes.VotingResult(ballot); err != nil {
		return
	}

	if vr.IsClosed() || !vr.CanGetResult(is.VotingThresholdPolicy) {
		return
	}

	votingHole, state, ended := vr.MakeResult(is.VotingThresholdPolicy)
	if !ended {
		return
	}

	if vs, err = vr.ChangeState(votingHole, state); err != nil {
		return
	}

	return
}
