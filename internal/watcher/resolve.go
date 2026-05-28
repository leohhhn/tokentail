package watcher

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

const minimalERC20ABI = `[
  {"name":"symbol",  "type":"function","inputs":[],"outputs":[{"name":"","type":"string"}],"stateMutability":"view"},
  {"name":"decimals","type":"function","inputs":[],"outputs":[{"name":"","type":"uint8"}], "stateMutability":"view"}
]`

// ResolveToken fetches symbol and decimals given an ERC-20 contract address on-chain.
func ResolveToken(ctx context.Context, client *ethclient.Client, addr common.Address) (Token, error) {
	parsed, err := abi.JSON(strings.NewReader(minimalERC20ABI))
	if err != nil {
		return Token{}, fmt.Errorf("parse abi: %w", err)
	}

	symbol, err := callString(ctx, client, addr, parsed, "symbol")
	if err != nil {
		return Token{}, fmt.Errorf("symbol(): %w", err)
	}

	decimals, err := callUint8(ctx, client, addr, parsed, "decimals")
	if err != nil {
		return Token{}, fmt.Errorf("decimals(): %w", err)
	}

	return Token{
		Symbol:   symbol,
		Address:  addr,
		Decimals: decimalsToFactor(decimals),
	}, nil
}

// callString executes a read-only (view) contract call that returns a single
// string value. It ABI-encodes the call using the provided parsed ABI and
// method name, sends it via eth_call at the latest block, then ABI-decodes
// the raw response bytes back into a Go string.
//
// The caller is responsible for ensuring that method exists in parsed and that
// its first return value is of ABI type "string"; a type mismatch will panic
// at the type assertion on the decoded value.
//
// Returns an error if ABI encoding fails, the RPC call is rejected (e.g. the
// address is not a contract, or the node returned a revert), or decoding the
// response produces no values.
func callString(ctx context.Context, client *ethclient.Client, addr common.Address, parsed abi.ABI, method string) (string, error) {
	data, err := parsed.Pack(method)
	if err != nil {
		return "", err
	}

	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: data}, nil)
	if err != nil {
		return "", err
	}

	values, err := parsed.Unpack(method, result)
	if err != nil || len(values) == 0 {
		return "", fmt.Errorf("unpack: %w", err)
	}

	return values[0].(string), nil
}

// callUint8 executes a read-only (view) contract call that returns a single
// uint8 value. It follows the same encode → eth_call → decode pipeline as
// callString, but asserts the first decoded value as uint8.
//
// uint8 is the standard ABI type for ERC-20 decimals(), which is its primary
// use here. The maximum representable value (255) comfortably covers all
// practical token precisions.
//
// The same caveats apply as callString: method must exist in parsed, its first
// return type must be "uint8", and a type mismatch will panic at the assertion.
func callUint8(ctx context.Context, client *ethclient.Client, addr common.Address, parsed abi.ABI, method string) (uint8, error) {
	data, err := parsed.Pack(method)
	if err != nil {
		return 0, err
	}
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: data}, nil)
	if err != nil {
		return 0, err
	}
	values, err := parsed.Unpack(method, result)
	if err != nil || len(values) == 0 {
		return 0, fmt.Errorf("unpack: %w", err)
	}
	return values[0].(uint8), nil
}
