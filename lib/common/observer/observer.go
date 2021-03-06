package observer

import (
	"github.com/GianlucaGuarini/go-observable"
	"strings"
)

var SyncBlockWaitObserver = observable.New()

var ResourceObserver = observable.New()

// The type of ressource that the event concerns
type ResourceType = string

// The type of key that the event applies to (depends on the ressource)
type KeyType = string

const (
	// An event relative to transactions
	Tx ResourceType = "tx"
	// An event relative to transactions in the transaction pool
	TxPool = "txpool"
	// An event relative to accounts (creation, update)
	Acc = "acc"
)

const (
	// All events related to the `ResourceType`
	All KeyType = "*"
	// "Identifier" of the item
	// Hash for a Transaction, address for an Account.
	Identifier = "identifier"
	// Tx/TxPool only: Transactions with a specified source
	Source = "source"
	// Tx/TxPool only: Transactions with a specified target
	Target = "target"
)

// A Condition can be sent as the body when calling subscribe
type Condition struct {
	// Affected ressource
	Resource ResourceType `json:"resource"`
	// Filter to use (can be `All)
	Key KeyType `json:"key"`
	// If `Key != All`, value of the filter
	Value string `json:"value"`
}

//
// Instantiate a new `Condition` object
//
// Params:
//     ressource = The requested `RessourceType` to stream
//     key       = The key to stream
//     v         = An optional value for key. Only the first value will be used.
//
// Returns: A `Condition` object, that can be triggered or passed to `/subscribe`
//
func NewCondition(resource ResourceType, key KeyType, v ...string) Condition {
	value := ""
	if len(v) > 0 {
		value = v[0]
	}
	return Condition{
		Resource: resource,
		Key:      key,
		Value:    value,
	}
}

// Implement `fmt.Stringer`
func (c Condition) String() string {
	toStr := c.Resource + "-"
	if c.Key == All {
		toStr += c.Key
	} else {
		toStr += c.Key + "="
		toStr += c.Value
	}
	return toStr
}

// An array of Condition
type Conditions []Condition

// Implement `fmt.Stringer`
func (cs Conditions) String() string {
	var ss []string
	for _, c := range cs {
		ss = append(ss, c.String())
	}
	return strings.Join(ss, "&")
}
