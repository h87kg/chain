/*
Package protocol provides the logic to tie together
storage and validation for a Chain Protocol blockchain.

This comprises all behavior that's common to every full
node, as well as other functions that need to operate on the
blockchain state.

Here are a few examples of typical full node types.

Proposer

A proposer has two basic jobs: collecting transactions from
other nodes and putting them into blocks.

To add a transaction to the pending transaction pool, call
AddTx for each one.

To add a new block to the blockchain, call ProposeBlock,
sign the block (possibly collecting signatures from other
parties), and call CommitBlock.

Signer

A signer validates blocks supplied by the proposer and signs
at most one block at each height.

Participant

A participant node in a network may select outputs for spending
and compose transactions.

To publish a new transaction, prepare your transaction
(select outputs, and compose and sign the tx) and send the
transaction to the network's proposer. To wait for
confirmation, call WaitForBlock on successive block heights
and inspect the blockchain state until you find that the
transaction has been either confirmed or rejected. Note
that transactions may be malleable if there's no commitment
to TXSIGHASH.

To ingest a block, call ValidateBlock and CommitBlock.
*/
package protocol

import (
	"context"
	"sync"
	"time"

	"github.com/golang/groupcache/lru"

	"chain/errors"
	"chain/log"
	"chain/protocol/bc"
	"chain/protocol/state"
)

// maxCachedValidatedTxs is the max number of validated txs to cache.
const maxCachedValidatedTxs = 1000

var (
	// ErrTheDistantFuture is returned when waiting for a blockheight
	// too far in excess of the tip of the blockchain.
	ErrTheDistantFuture = errors.New("block height too far in future")
)

type BlockCallback func(ctx context.Context, block *bc.Block) error

// Store provides storage for blockchain data: blocks and state tree
// snapshots.
//
// Note, this is different from a state snapshot. A state snapshot
// provides access to the state at a given point in time -- outputs
// and issuance memory. The Chain type uses Store to load state
// from storage and persist validated data.
type Store interface {
	Height(context.Context) (uint64, error)
	GetBlock(context.Context, uint64) (*bc.Block, error)
	LatestSnapshot(context.Context) (*state.Snapshot, uint64, error)

	SaveBlock(context.Context, *bc.Block) error
	FinalizeBlock(context.Context, uint64) error
	SaveSnapshot(context.Context, uint64, *state.Snapshot) error
}

// Pool provides storage for transactions in the pending
// transaction pool.
type Pool interface {
	// Insert adds a transaction to the pool.
	// It doesn't check for validity, or whether the transaction
	// conflicts with another.
	// It is required to be idempotent.
	Insert(context.Context, *bc.Tx) error

	// Dump wipes the pending transaction pool and returns all
	// transactions that were in the pool.
	Dump(context.Context) ([]*bc.Tx, error)
}

// Chain provides a complete, minimal blockchain database. It
// delegates the underlying storage to other objects, and uses
// validation logic from package validation to decide what
// objects can be safely stored.
type Chain struct {
	InitialBlockHash  bc.Hash
	MaxIssuanceWindow time.Duration // only used by generators

	blockCallbacks []BlockCallback
	state          struct {
		cond     sync.Cond // protects height, block, snapshot
		height   uint64
		block    *bc.Block       // current only if leader
		snapshot *state.Snapshot // current only if leader
	}
	store Store
	pool  Pool

	lastQueuedSnapshot time.Time
	pendingSnapshots   chan pendingSnapshot

	prevalidated prevalidatedTxsCache
}

type pendingSnapshot struct {
	height   uint64
	snapshot *state.Snapshot
}

// NewChain returns a new Chain using store as the underlying storage.
func NewChain(ctx context.Context, initialBlockHash bc.Hash, store Store, pool Pool, heights <-chan uint64) (*Chain, error) {
	c := &Chain{
		InitialBlockHash: initialBlockHash,
		store:            store,
		pool:             pool,
		pendingSnapshots: make(chan pendingSnapshot, 1),
		prevalidated: prevalidatedTxsCache{
			lru: lru.New(maxCachedValidatedTxs),
		},
	}
	c.state.cond.L = new(sync.Mutex)

	var err error
	c.state.height, err = store.Height(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "looking up blockchain height")
	}

	// Note that c.height.n may still be zero here.
	if heights != nil {
		go func() {
			for h := range heights {
				c.setHeight(h)
			}
		}()
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ps := <-c.pendingSnapshots:
				err = store.SaveSnapshot(ctx, ps.height, ps.snapshot)
				if err != nil {
					log.Error(ctx, err, "at", "saving snapshot")
				}
			}
		}
	}()

	return c, nil
}

// Height returns the current height of the blockchain.
func (c *Chain) Height() uint64 {
	c.state.cond.L.Lock()
	defer c.state.cond.L.Unlock()
	return c.state.height
}

// Store returns the Store used by the blockchain.
func (c *Chain) Store() Store {
	return c.store
}

// State returns the most recent state available. It will not be current
// unless the current process is the leader. Callers should examine the
// returned block header's height if they need to verify the current state.
func (c *Chain) State() (*bc.Block, *state.Snapshot) {
	c.state.cond.L.Lock()
	defer c.state.cond.L.Unlock()
	return c.state.block, c.state.snapshot
}

func (c *Chain) setState(b *bc.Block, s *state.Snapshot) {
	c.state.cond.L.Lock()
	defer c.state.cond.L.Unlock()
	c.state.block = b
	c.state.snapshot = s
	if b != nil && b.Height > c.state.height {
		c.state.height = b.Height
		c.state.cond.Broadcast()
	}
}

func (c *Chain) AddBlockCallback(f BlockCallback) {
	c.blockCallbacks = append(c.blockCallbacks, f)
}

// WaitForBlockSoon waits for the block at the given height,
// but it is an error to wait for a block far in the future.
// WaitForBlockSoon will timeout if the context times out.
// To wait unconditionally, the caller should use WaitForBlock.
func (c *Chain) WaitForBlockSoon(ctx context.Context, height uint64) error {
	const slop = 3
	if height > c.Height()+slop {
		return ErrTheDistantFuture
	}

	done := make(chan struct{}, 1)
	go func() {
		c.WaitForBlock(height)
		done <- struct{}{}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// WaitForBlock waits for the block at the given height.
func (c *Chain) WaitForBlock(height uint64) {
	c.state.cond.L.Lock()
	defer c.state.cond.L.Unlock()
	for c.state.height < height {
		c.state.cond.Wait()
	}
}
