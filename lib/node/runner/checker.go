package runner

import (
	"bufio"
	"bytes"
	"io"

	"boscoin.io/sebak/lib/ballot"
	"boscoin.io/sebak/lib/block"
	"boscoin.io/sebak/lib/common"
	"boscoin.io/sebak/lib/consensus"
	"boscoin.io/sebak/lib/errors"
	"boscoin.io/sebak/lib/node"
	"boscoin.io/sebak/lib/storage"
	"boscoin.io/sebak/lib/transaction"
	"boscoin.io/sebak/lib/transaction/operation"
	"boscoin.io/sebak/lib/voting"
	logging "github.com/inconshreveable/log15"
)

type CheckerStopCloseConsensus struct {
	checker *BallotChecker
	message string
}

func NewCheckerStopCloseConsensus(checker *BallotChecker, message string) CheckerStopCloseConsensus {
	return CheckerStopCloseConsensus{
		checker: checker,
		message: message,
	}
}

func (c CheckerStopCloseConsensus) Error() string {
	return c.message
}

func (c CheckerStopCloseConsensus) Checker() common.Checker {
	return c.checker
}

type BallotChecker struct {
	common.DefaultChecker

	NodeRunner           *NodeRunner
	LocalNode            *node.LocalNode
	NetworkID            []byte
	Message              common.NetworkMessage
	IsNew                bool
	Ballot               ballot.Ballot
	VotingHole           voting.Hole
	Result               consensus.RoundVoteResult
	VotingFinished       bool
	FinishedVotingHole   voting.Hole
	LatestUpdatedSources map[string]struct{}

	Log logging.Logger
}

// BallotUnmarshal makes `Ballot` from common.NetworkMessage.
func BallotUnmarshal(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)

	var b ballot.Ballot
	if b, err = ballot.NewBallotFromJSON(checker.Message.Data); err != nil {
		return
	}

	if err = b.IsWellFormed(checker.NetworkID, checker.NodeRunner.Conf); err != nil {
		return
	}

	checker.Ballot = b
	checker.Log = checker.Log.New(logging.Ctx{
		"ballot":      checker.Ballot.GetHash(),
		"state":       checker.Ballot.State(),
		"proposer":    checker.Ballot.Proposer(),
		"votingBasis": checker.Ballot.VotingBasis(),
		"from":        checker.Ballot.Source(),
		"vote":        checker.Ballot.Vote(),
	})
	checker.Log.Debug("message is verified")

	return
}

// BallotValidateOperationBodyCollectTxFee validates
// `CollectTxFee`.
func BallotValidateOperationBodyCollectTxFee(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)

	var opb operation.CollectTxFee
	if opb, err = checker.Ballot.ProposerTransaction().CollectTxFee(); err != nil {
		return
	}

	// check common account
	if opb.Target != checker.NodeRunner.CommonAccountAddress {
		err = errors.InvalidOperation
		return
	}

	return
}

// BallotValidateOperationBodyInflation validates `Inflation`
func BallotValidateOperationBodyInflation(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)

	var opb operation.Inflation
	if opb, err = checker.Ballot.ProposerTransaction().Inflation(); err != nil {
		return
	}

	// check common account
	if opb.Target != checker.NodeRunner.CommonAccountAddress {
		err = errors.InvalidOperation
		return
	}
	if opb.InitialBalance != checker.NodeRunner.InitialBalance {
		err = errors.InvalidOperation
		return
	}

	if opb.Ratio != common.InflationRatioString {
		err = errors.InvalidOperation
		return
	}

	var expectedInflation common.Amount
	if checker.NodeRunner.Consensus().LatestBlock().Height <= common.BlockHeightEndOfInflation {
		expectedInflation, err = common.CalculateInflation(checker.NodeRunner.InitialBalance)
		if err != nil {
			return
		}
	}

	if opb.Amount != expectedInflation {
		err = errors.InvalidOperation
		return
	}

	return
}

// BallotNotFromKnownValidators checks the incoming ballot
// is from the known validators.
func BallotNotFromKnownValidators(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)
	if checker.LocalNode.HasValidators(checker.Ballot.Source()) {
		return
	}

	checker.Log.Debug("ballot from unknown validator")

	err = errors.BallotFromUnknownValidator
	return
}

// BallotCheckSYNC performs sync by considering sync condition.
// And to participate in the consensus,
// update the latestblock by referring to the database.
func BallotCheckSYNC(c common.Checker, args ...interface{}) error {
	checker := c.(*BallotChecker)
	var err error

	is := checker.NodeRunner.Consensus()
	b := checker.Ballot
	latestHeight := is.LatestBlock().Height
	if latestHeight >= b.VotingBasis().Height { // in consensus, not sync
		return nil
	}

	if !isBallotAcceptYes(b) {
		return nil
	}

	if !hasBallotValidProposer(is, b) {
		return nil
	}

	if is.LatestBallot.H.Hash == "" {
		is.LatestBallot = b
	}

	is.SaveNodeHeight(b.Source(), b.VotingBasis().Height)

	var syncHeight uint64
	var nodeAddrs []string
	syncHeight, nodeAddrs, err = checker.NodeRunner.Consensus().GetSyncInfo()
	if err != nil {
		return err
	}

	defer func() {
		if b.VotingBasis().Height == syncHeight {
			is.LatestBallot = b
		}
	}()

	if latestHeight < syncHeight-1 { // request sync until syncHeight
		checker.NodeRunner.Log().Debug("latestHeight < syncHeight-1", "latestHeight", latestHeight, "syncHeight", syncHeight)
		is.StartSync(syncHeight, nodeAddrs)
		return NewCheckerStopCloseConsensus(checker, "ballot makes node in sync")
	} else {
		if latestHeight == syncHeight-1 { // finish previous and current height ballot
			_, err = finishBallot(
				checker.NodeRunner.Storage(),
				is.LatestBallot,
				checker.NodeRunner.TransactionPool,
				checker.Log,
				checker.NodeRunner.Log(),
			)
			if err != nil {
				return err
			}
		}

		_, err = finishBallot(
			checker.NodeRunner.Storage(),
			checker.Ballot,
			checker.NodeRunner.TransactionPool,
			checker.Log,
			checker.NodeRunner.Log(),
		)
		if err != nil {
			return err
		}

		checker.LocalNode.SetConsensus()
		checker.NodeRunner.TransitISAACState(b.VotingBasis(), ballot.StateALLCONFIRM)
		return NewCheckerStopCloseConsensus(checker, "ballot got consensus")
	}
}

func isBallotAcceptYes(b ballot.Ballot) bool {
	return b.State() == ballot.StateACCEPT && b.Vote() == voting.YES
}

func hasBallotValidProposer(is *consensus.ISAAC, b ballot.Ballot) bool {
	return b.Proposer() == is.SelectProposer(b.VotingBasis().Height, b.VotingBasis().Round)
}

// BallotAlreadyFinished checks the incoming ballot in
// valid round.
func BallotAlreadyFinished(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)
	ballotRound := checker.Ballot.VotingBasis()
	if !checker.NodeRunner.Consensus().IsAvailableRound(
		ballotRound,
		block.GetLatestBlock(checker.NodeRunner.Storage()),
	) {
		err = errors.BallotAlreadyFinished
		checker.Log.Debug("ballot already finished")
		return
	}

	return
}

// BallotAlreadyVoted checks the node of ballot voted.
func BallotAlreadyVoted(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)
	if checker.NodeRunner.Consensus().IsVoted(checker.Ballot) {
		err = errors.BallotAlreadyVoted
	}

	return
}

// BallotVote vote by incoming ballot; if the ballot is new
// and the round of ballot is not yet registered, this will make new
// `RunningRound`.
func BallotVote(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)

	checker.IsNew, err = checker.NodeRunner.Consensus().Vote(checker.Ballot)
	checker.Log.Debug("ballot voted", "ballot", checker.Ballot, "new", checker.IsNew)

	return
}

// BallotIsSameProposer checks the incoming ballot has the
// same proposer with the current `RunningRound`.
func BallotIsSameProposer(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)

	if checker.VotingHole != voting.NOTYET {
		return
	}

	if checker.Ballot.IsFromProposer() && checker.Ballot.Source() == checker.NodeRunner.Node().Address() {
		return
	}

	if !checker.NodeRunner.Consensus().HasRunningRound(checker.Ballot.VotingBasis().Index()) {
		err = errors.New("`RunningRound` not found")
		return
	}

	if !checker.NodeRunner.Consensus().HasSameProposer(checker.Ballot) {
		checker.VotingHole = voting.NO
		checker.Log.Debug("ballot has different proposer", "proposer", checker.Ballot.Proposer())
		return
	}

	return
}

// BallotCheckResult checks the voting result.
func BallotCheckResult(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)

	if !checker.Ballot.State().IsValidForVote() {
		return
	}

	result, votingHole, finished := checker.NodeRunner.Consensus().CanGetVotingResult(checker.Ballot)

	checker.Result = result
	checker.VotingFinished = finished
	checker.FinishedVotingHole = votingHole

	if checker.VotingFinished {
		checker.Log.Debug(
			"get result",
			"finished voting.Hole", checker.FinishedVotingHole,
			"result", checker.Result,
		)
	}

	return
}

// insertMissingTransaction will get the missing tranactions, that is, not in
// `TransactionPool` from proposer.
func insertMissingTransaction(checker *BallotChecker) (err error) {
	// get missing transactions
	var unknown []string
	var exists bool
	for _, hash := range checker.Ballot.Transactions() {
		if checker.NodeRunner.TransactionPool.Has(hash) {
			continue
		}
		if exists, err = block.ExistsTransactionPool(checker.NodeRunner.Storage(), hash); err != nil {
			return
		} else if exists {
			continue
		}
		unknown = append(unknown, hash)
	}

	if len(unknown) < 1 {
		return
	}

	client := checker.NodeRunner.ConnectionManager().GetConnection(checker.Ballot.Proposer())
	if client == nil {
		err = errors.BallotFromUnknownValidator
		return
	}
	var body []byte
	// TODO check error
	if body, err = client.GetTransactions(unknown); err != nil {
		return
	}

	var receivedTransaction []transaction.Transaction
	bf := bufio.NewReader(bytes.NewReader(body))
	for {
		var l []byte
		l, err = bf.ReadBytes('\n')
		if err == io.EOF {
			err = nil
			break
		} else if err != nil {
			return
		}
		var itemType NodeItemDataType
		var d interface{}
		if itemType, d, err = UnmarshalNodeItemResponse(l); err != nil {
			return
		}
		if itemType == NodeItemError {
			err = d.(*errors.Error)
			return
		}

		var tx transaction.Transaction
		var ok bool
		if tx, ok = d.(transaction.Transaction); !ok {
			err = errors.TransactionNotFound
			return
		}
		if err = tx.IsWellFormed(checker.NetworkID, checker.NodeRunner.Conf); err != nil {
			return
		}

		if err = ValidateTx(checker.NodeRunner.Storage(), tx); err != nil {
			return
		}

		receivedTransaction = append(receivedTransaction, tx)
	}

	var bs *storage.LevelDBBackend
	bs, err = checker.NodeRunner.Storage().OpenBatch()
	for _, tx := range receivedTransaction {
		if _, err = block.SaveTransactionPool(bs, tx); err != nil {
			return
		}
	}
	if err = bs.Commit(); err != nil {
		bs.Discard()
		return
	}

	return
}

func BallotGetMissingTransaction(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)

	if checker.VotingHole != voting.NOTYET {
		return
	}

	if err = insertMissingTransaction(checker); err != nil {
		checker.VotingHole = voting.NO
		checker.Log.Debug("failed to get the missing transactions of ballot", "error", err)
		err = nil
	}

	return
}

var INITBallotTransactionCheckerFuncs = []common.CheckerFunc{
	IsNew,
	CheckMissingTransaction,
	BallotTransactionsSameSource,
	BallotTransactionsOperationBodyCollectTxFee,
	BallotTransactionsAllValid,
}

// INITBallotValidateTransactions validates the
// transactions of newly added ballot.
func INITBallotValidateTransactions(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)

	if checker.VotingFinished {
		return
	}
	var voted bool
	voted, err = checker.NodeRunner.Consensus().IsVotedByNode(checker.Ballot, checker.LocalNode.Address())
	if voted || err != nil {
		err = errors.BallotAlreadyVoted
		return
	}

	if checker.VotingHole != voting.NOTYET {
		return
	}

	transactionsChecker := &BallotTransactionChecker{
		DefaultChecker: common.DefaultChecker{Funcs: INITBallotTransactionCheckerFuncs},
		NodeRunner:     checker.NodeRunner,
		LocalNode:      checker.LocalNode,
		NetworkID:      checker.NetworkID,
		Ballot:         checker.Ballot,
		Transactions:   checker.Ballot.Transactions(),
		VotingHole:     voting.NOTYET,
		transactionCache: NewTransactionCache(
			checker.NodeRunner.Storage(),
			checker.NodeRunner.TransactionPool,
		),
	}

	err = common.RunChecker(transactionsChecker, common.DefaultDeferFunc)
	if err != nil {
		if _, ok := err.(common.CheckerErrorStop); !ok {
			checker.VotingHole = voting.NO
			checker.Log.Debug("failed to handle transactions of ballot", "error", err)
			err = nil
			return
		}
		err = nil
	}

	if transactionsChecker.VotingHole == voting.NO {
		checker.VotingHole = voting.NO
	} else {
		checker.VotingHole = voting.YES
	}

	return
}

// SIGNBallotBroadcast will broadcast the validated SIGN ballot.
func SIGNBallotBroadcast(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)

	newBallot := checker.Ballot
	newBallot.SetSource(checker.LocalNode.Address())
	newBallot.SetVote(ballot.StateSIGN, checker.VotingHole)
	newBallot.Sign(checker.LocalNode.Keypair(), checker.NetworkID)

	if !checker.NodeRunner.Consensus().HasRunningRound(checker.Ballot.VotingBasis().Index()) {
		err = errors.New("RunningRound not found")
		return

	}
	checker.NodeRunner.ConnectionManager().Broadcast(newBallot)
	checker.Log.Debug("ballot will be broadcasted", "newBallot", newBallot)

	return
}

// TransitStateToSIGN changes ISAACState to SIGN
func TransitStateToSIGN(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)
	checker.NodeRunner.TransitISAACState(checker.Ballot.VotingBasis(), ballot.StateSIGN)

	return
}

// ACCEPTBallotBroadcast will broadcast the confirmed ACCEPT
// ballot.
func ACCEPTBallotBroadcast(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)
	if !checker.VotingFinished {
		return
	}

	newBallot := checker.Ballot
	newBallot.SetSource(checker.LocalNode.Address())
	newBallot.SetVote(ballot.StateACCEPT, checker.FinishedVotingHole)
	newBallot.Sign(checker.LocalNode.Keypair(), checker.NetworkID)

	if !checker.NodeRunner.Consensus().HasRunningRound(checker.Ballot.VotingBasis().Index()) {
		err = errors.New("RunningRound not found")
		return

	}
	checker.NodeRunner.ConnectionManager().Broadcast(newBallot)
	checker.Log.Debug("ballot will be broadcasted", "newBallot", newBallot)

	return
}

// TransitStateToACCEPT changes ISAACState to ACCEPT
func TransitStateToACCEPT(c common.Checker, args ...interface{}) (err error) {
	checker := c.(*BallotChecker)
	if !checker.VotingFinished {
		return
	}
	checker.NodeRunner.TransitISAACState(checker.Ballot.VotingBasis(), ballot.StateACCEPT)

	return
}

// FinishedBallotStore will store the confirmed ballot to
// `Block`.
func FinishedBallotStore(c common.Checker, args ...interface{}) error {
	checker := c.(*BallotChecker)

	if !checker.VotingFinished {
		return nil
	}

	basis := checker.Ballot.VotingBasis()

	var err error
	switch checker.FinishedVotingHole {
	case voting.YES:
		if err = saveBlock(checker); err != nil {
			return err
		}
		checker.NodeRunner.TransitISAACState(checker.Ballot.VotingBasis(), ballot.StateALLCONFIRM)
		checker.NodeRunner.Consensus().SetLatestVotingBasis(basis)

		reorganizeTransactionPool(checker)
		checker.NodeRunner.Consensus().RemoveRunningRoundsWithSameHeight(basis.Height)

		err = NewCheckerStopCloseConsensus(checker, "ballot got consensus and will be stored")
	case voting.NO, voting.EXP:
		checker.NodeRunner.isaacStateManager.IncreaseRound()
		checker.NodeRunner.Consensus().SetLatestVotingBasis(basis)

		checker.NodeRunner.Consensus().RemoveRunningRoundsWithSameHeight(basis.Height)

		err = NewCheckerStopCloseConsensus(checker, "ballot got consensus")
	case voting.NOTYET:
		return errors.New("invalid voting.Hole, `NOTYET`")
	}

	return err
}

func saveBlock(checker *BallotChecker) error {
	var err error
	if err = insertMissingTransaction(checker); err != nil {
		checker.Log.Debug("failed to get the missing transactions of ballot", "error", err)
		return err
	}

	var bs *storage.LevelDBBackend
	if bs, err = checker.NodeRunner.Storage().OpenBatch(); err != nil {
		return err
	}

	proposedTransactions, err := getProposedTransactions(
		bs,
		checker.Ballot.B.Proposed.Transactions,
		checker.NodeRunner.TransactionPool,
	)
	if err != nil {
		return err
	}

	for _, tx := range proposedTransactions {
		checker.LatestUpdatedSources[tx.B.Source] = struct{}{}
	}

	var theBlock *block.Block
	theBlock, err = finishBallotWithProposedTxs(
		bs,
		checker.Ballot,
		checker.NodeRunner.TransactionPool.Pending,
		proposedTransactions,
		checker.Log,
		checker.NodeRunner.Log(),
	)
	if err != nil {
		bs.Discard()
		checker.Log.Error("failed to finish ballot", "error", err)
		return err
	}

	if err = bs.Commit(); err != nil {
		if err != errors.NotCommittable {
			bs.Discard()
			return err
		}
	}

	checker.Log.Debug("ballot was stored", "block", *theBlock)
	checker.NodeRunner.SavingBlockOperations().Save(*theBlock)

	return nil
}

func reorganizeTransactionPool(checker *BallotChecker) error {
	basisIndex := checker.Ballot.VotingBasis().Index()
	proposer := checker.Ballot.Proposer()
	transactionPool := checker.NodeRunner.TransactionPool
	rr, found := checker.NodeRunner.Consensus().RunningRounds[basisIndex]
	if found {
		transactionPool.Remove(rr.Transactions[proposer]...)
	}

	invalids := []string{}
	for source := range checker.LatestUpdatedSources {
		tx, found := transactionPool.GetFromSource(source)
		if !found {
			continue
		}
		if err := ValidateTx(checker.NodeRunner.Storage(), tx); err != nil {
			invalids = append(invalids, tx.GetHash())
		}
	}
	transactionPool.Remove(invalids...)

	return nil
}

func isValidRound(st *storage.LevelDBBackend, r voting.Basis, log logging.Logger) (bool, error) {
	latestBlock := block.GetLatestBlock(st)
	if latestBlock.Height != r.Height {
		log.Error(
			"ballot height is not equal to latestBlock",
			"in ballot", r.Height,
			"latest height", latestBlock.Height,
		)
		return false, errors.New("ballot height is not equal to latestBlock")
	}
	if latestBlock.Hash != r.BlockHash {
		log.Error(
			"latest block hash in ballot is not equal to latestBlock",
			"in ballot", r.BlockHash,
			"latest block", latestBlock.Hash,
		)
		return false, errors.New("latest block hash in ballot is not equal to latestBlock")
	}

	return true, nil
}
