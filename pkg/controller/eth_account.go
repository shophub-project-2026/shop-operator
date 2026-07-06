package controller

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"
)

// ethAccount is a freshly generated Ethereum account: the EIP-55 checksummed
// address plus the hex-encoded secp256k1 private key it is derived from.
type ethAccount struct {
	Address    string
	PrivateKey string
}

// generateEthAccount creates a new Ethereum account. Ethereum accounts exist
// implicitly on-chain the moment a keypair is generated — deriving the address
// from a new secp256k1 key IS account creation; no transaction is required for
// the account to be able to receive funds.
func generateEthAccount() (*ethAccount, error) {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generate secp256k1 key: %w", err)
	}
	return &ethAccount{
		Address:    ethAddressFromPubKey(priv.PubKey()),
		PrivateKey: hex.EncodeToString(priv.Serialize()),
	}, nil
}

// ethAddressFromPubKey derives the Ethereum address: keccak256 over the
// uncompressed public key (without the 0x04 prefix byte), last 20 bytes.
func ethAddressFromPubKey(pub *secp256k1.PublicKey) string {
	uncompressed := pub.SerializeUncompressed() // 65 bytes, leading 0x04
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	digest := h.Sum(nil)
	return toEIP55("0x" + hex.EncodeToString(digest[12:]))
}

// toEIP55 applies the EIP-55 mixed-case checksum to a 0x-prefixed lowercase
// hex address: hex letters are uppercased when the corresponding nibble of
// keccak256(lowercase address without prefix) is >= 8.
func toEIP55(address string) string {
	addr := strings.ToLower(strings.TrimPrefix(address, "0x"))
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(addr))
	digest := hex.EncodeToString(h.Sum(nil))

	out := make([]byte, len(addr))
	for i := 0; i < len(addr); i++ {
		c := addr[i]
		if c >= 'a' && c <= 'f' && digest[i] >= '8' {
			c = c - 'a' + 'A'
		}
		out[i] = c
	}
	return "0x" + string(out)
}

// BalanceFetcher reads the on-chain balance of an account. Implemented by
// ethRPCClient; kept as an interface so unit tests can stub it and so the
// reconciler degrades gracefully when no RPC endpoint is configured.
type BalanceFetcher interface {
	Balance(ctx context.Context, address string) (*big.Int, error)
}

// ethRPCClient fetches balances over plain JSON-RPC (eth_getBalance). A full
// go-ethereum dependency is deliberately avoided in the operator image.
type ethRPCClient struct {
	url    string
	client *http.Client
}

// NewEthRPCClient returns a BalanceFetcher talking to the given JSON-RPC URL.
func NewEthRPCClient(url string) BalanceFetcher {
	return &ethRPCClient{
		url:    url,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *ethRPCClient) Balance(ctx context.Context, address string) (*big.Int, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "eth_getBalance",
		"params":  []string{address, "latest"},
		"id":      1,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eth_getBalance request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode eth_getBalance response: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("eth_getBalance: %s", out.Error.Message)
	}

	wei, ok := new(big.Int).SetString(strings.TrimPrefix(out.Result, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("eth_getBalance: unparsable result %q", out.Result)
	}
	return wei, nil
}

// formatEth renders a Wei amount as a human-readable ETH string for status.
func formatEth(wei *big.Int) string {
	eth := new(big.Float).Quo(new(big.Float).SetInt(wei), big.NewFloat(1e18))
	return eth.Text('f', 6) + " ETH"
}
