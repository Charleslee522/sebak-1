package sebak

import (
	"encoding/json"
	"fmt"

	"github.com/btcsuite/btcutil/base58"

	"boscoin.io/sebak/lib/common"
	"boscoin.io/sebak/lib/error"
	"boscoin.io/sebak/lib/storage"
)

const (
	BlockPrefixHash      string = "b-hash-"      // b-hash-<Block.Hash>
	BlockPrefixConfirmed string = "b-confirmed-" // b-hash-<Block.Confirmed>
)

type Block struct {
	Hash         string
	PreviousHash string

	Height    uint64
	Confirmed string

	Transactions []string /* []Transaction.GetHash() */
	Proposer     string   /* Node.Address() */
}

func NewBlock(propser string, round Round, transactions []string, confirmed string) Block {
	b := &Block{
		Height:       round.BlockHeight + 1,
		PreviousHash: round.BlockHash,
		Transactions: transactions,
		Proposer:     propser,
		Confirmed:    confirmed,
	}

	b.Hash = base58.Encode(sebakcommon.MustMakeObjectHash(b))

	return *b
}

func GetBlockKey(hash string) string {
	return fmt.Sprintf("%s%s", BlockPrefixHash, hash)
}

func GetBlockKeyPrefixConfirmed(confirmed string) string {
	return fmt.Sprintf("%s%s-", BlockPrefixConfirmed, confirmed)
}

func (b Block) NewBlockKeyConfirmed() string {
	return fmt.Sprintf(
		"%s%s",
		GetBlockKeyPrefixConfirmed(b.Confirmed),
		sebakcommon.GetUniqueIDFromUUID(),
	)
}

func (b Block) Save(st *sebakstorage.LevelDBBackend) (err error) {
	key := GetBlockKey(b.Hash)

	var exists bool
	exists, err = st.Has(key)
	if err != nil {
		return
	} else if exists {
		return sebakerror.ErrorBlockAlreadyExists
	}

	if err = st.New(key, b); err != nil {
		return
	}

	if err = st.New(b.NewBlockKeyConfirmed(), b.Hash); err != nil {
		return
	}

	return
}

func GetBlock(st *sebakstorage.LevelDBBackend, hash string) (bt Block, err error) {
	err = st.Get(GetBlockKey(hash), &bt)
	return
}

func LoadBlocksInsideIterator(
	st *sebakstorage.LevelDBBackend,
	iterFunc func() (sebakstorage.IterItem, bool),
	closeFunc func(),
) (
	func() (Block, bool),
	func(),
) {

	return (func() (Block, bool) {
			item, hasNext := iterFunc()
			if !hasNext {
				return Block{}, false
			}

			var hash string
			json.Unmarshal(item.Value, &hash)

			b, err := GetBlock(st, hash)
			if err != nil {
				return Block{}, false
			}

			return b, hasNext
		}), (func() {
			closeFunc()
		})
}

func GetBlocksByConfirmed(st *sebakstorage.LevelDBBackend, reverse bool) (
	func() (Block, bool),
	func(),
) {
	iterFunc, closeFunc := st.GetIterator(BlockPrefixConfirmed, reverse)

	return LoadBlocksInsideIterator(st, iterFunc, closeFunc)
}
