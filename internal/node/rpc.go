// Package node talks to a Bitcoin Core / Bitcoin Cash Node full node over
// JSON-RPC and listens for new blocks via ZMQ.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RPCError is an error object returned by the node.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Client is a JSON-RPC 1.0 client (the dialect bitcoind speaks).
type Client struct {
	url        string
	user, pass string
	cookieFile string
	timeout    time.Duration
	http       *http.Client
	id         atomic.Uint64

	mu         sync.Mutex
	cookieAuth string
}

// NewClient creates a client. If cookieFile is set it is used whenever
// user/pass are empty, and re-read after an authentication failure (bitcoind
// rotates the cookie on restart).
func NewClient(url, user, pass, cookieFile string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		url: url, user: user, pass: pass, cookieFile: cookieFile, timeout: timeout,
		http: &http.Client{Transport: &http.Transport{
			Proxy:               nil, // never send node credentials through a proxy
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
		}},
	}
}

func (c *Client) auth(reload bool) (string, string, error) {
	if c.user != "" || c.cookieFile == "" {
		return c.user, c.pass, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cookieAuth == "" || reload {
		b, err := os.ReadFile(c.cookieFile)
		if err != nil {
			return "", "", fmt.Errorf("read rpc cookie: %w", err)
		}
		c.cookieAuth = strings.TrimSpace(string(b))
	}
	u, p, ok := strings.Cut(c.cookieAuth, ":")
	if !ok {
		return "", "", errors.New("malformed rpc cookie file")
	}
	return u, p, nil
}

// Call invokes method with params and decodes the result into out (if non-nil).
func (c *Client) Call(ctx context.Context, method string, params []any, out any) error {
	return c.CallTimeout(ctx, c.timeout, method, params, out)
}

// CallTimeout is Call with an explicit timeout.
func (c *Client) CallTimeout(ctx context.Context, timeout time.Duration, method string, params []any, out any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "1.0", "id": c.id.Add(1), "method": method, "params": params,
	})
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		err := c.do(ctx, timeout, method, body, out, attempt > 0)
		if errors.Is(err, errUnauthorized) && attempt == 0 && c.cookieFile != "" && c.user == "" {
			continue // cookie may have rotated
		}
		return err
	}
}

var errUnauthorized = errors.New("rpc unauthorized (check rpc credentials)")

func (c *Client) do(ctx context.Context, timeout time.Duration, method string, body []byte, out any, reloadAuth bool) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	u, p, err := c.auth(reloadAuth)
	if err != nil {
		return err
	}
	if u != "" || p != "" {
		req.SetBasicAuth(u, p)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		io.Copy(io.Discard, resp.Body)
		return errUnauthorized
	}
	// bitcoind returns HTTP 500/404 together with a JSON error body.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return fmt.Errorf("%s: read response: %w", method, err)
	}
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("%s: http %d: invalid json response: %.200s", method, resp.StatusCode, data)
	}
	if env.Error != nil {
		return fmt.Errorf("%s: %w", method, env.Error)
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("%s: decode result: %w", method, err)
		}
	}
	return nil
}

// ChainInfo is the subset of getblockchaininfo the engine uses.
type ChainInfo struct {
	Chain                string  `json:"chain"`
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	BestBlockHash        string  `json:"bestblockhash"`
	Difficulty           float64 `json:"difficulty"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
	VerificationProgress float64 `json:"verificationprogress"`
}

// Synced reports whether the node is safe to mine on.
func (ci *ChainInfo) Synced() bool {
	if ci.Blocks != ci.Headers {
		return false
	}
	// A fresh regtest chain reports IBD until a recent block exists; GBT is
	// still served there (Bitcoin Core) and mining is how it leaves IBD.
	return !ci.InitialBlockDownload || ci.Chain == "regtest"
}

// GetBlockchainInfo calls getblockchaininfo.
func (c *Client) GetBlockchainInfo(ctx context.Context) (*ChainInfo, error) {
	var ci ChainInfo
	return &ci, c.Call(ctx, "getblockchaininfo", nil, &ci)
}

// NetworkInfo is the subset of getnetworkinfo the engine uses.
type NetworkInfo struct {
	Version     int64  `json:"version"`
	Subversion  string `json:"subversion"`
	Connections int    `json:"connections"`
}

// GetNetworkInfo calls getnetworkinfo.
func (c *Client) GetNetworkInfo(ctx context.Context) (*NetworkInfo, error) {
	var ni NetworkInfo
	return &ni, c.Call(ctx, "getnetworkinfo", nil, &ni)
}

// TemplateTx is one transaction in a block template.
type TemplateTx struct {
	Data   string `json:"data"`
	TxID   string `json:"txid"`
	Hash   string `json:"hash"`
	Fee    int64  `json:"fee"`
	SigOps int64  `json:"sigops"`
	Weight int64  `json:"weight"`
}

// BlockTemplate is a getblocktemplate result (BIP22/23, BIP145).
type BlockTemplate struct {
	Version                  int64        `json:"version"`
	Rules                    []string     `json:"rules"`
	PreviousBlockHash        string       `json:"previousblockhash"`
	Transactions             []TemplateTx `json:"transactions"`
	CoinbaseValue            int64        `json:"coinbasevalue"`
	Target                   string       `json:"target"`
	MinTime                  int64        `json:"mintime"`
	CurTime                  int64        `json:"curtime"`
	Bits                     string       `json:"bits"`
	Height                   int64        `json:"height"`
	DefaultWitnessCommitment string       `json:"default_witness_commitment"`
	LongPollID               string       `json:"longpollid"`
}

// GetBlockTemplate calls getblocktemplate with the given rules.
func (c *Client) GetBlockTemplate(ctx context.Context, rules []string) (*BlockTemplate, error) {
	req := map[string]any{}
	if len(rules) > 0 {
		req["rules"] = rules
	}
	var t BlockTemplate
	return &t, c.Call(ctx, "getblocktemplate", []any{req}, &t)
}

// SubmitBlock calls submitblock. It returns "" when the node accepted the
// block, otherwise the node's BIP22 reject reason (e.g. "duplicate",
// "inconclusive", "high-hash").
func (c *Client) SubmitBlock(ctx context.Context, blockHex string) (string, error) {
	var res *string
	// Large blocks take a while to validate; never cut submission short.
	if err := c.CallTimeout(ctx, 5*time.Minute, "submitblock", []any{blockHex}, &res); err != nil {
		return "", err
	}
	if res == nil {
		return "", nil
	}
	return *res, nil
}

// GetBestBlockHash calls getbestblockhash.
func (c *Client) GetBestBlockHash(ctx context.Context) (string, error) {
	var h string
	return h, c.Call(ctx, "getbestblockhash", nil, &h)
}

// BlockHeaderInfo is the subset of getblockheader (verbose) the engine uses.
type BlockHeaderInfo struct {
	Hash          string `json:"hash"`
	Confirmations int64  `json:"confirmations"`
	Height        int64  `json:"height"`
}

// GetBlockHeader calls getblockheader <hash> true.
func (c *Client) GetBlockHeader(ctx context.Context, hash string) (*BlockHeaderInfo, error) {
	var h BlockHeaderInfo
	return &h, c.Call(ctx, "getblockheader", []any{hash, true}, &h)
}

// ValidateAddressResult is the subset of validateaddress the engine uses.
type ValidateAddressResult struct {
	IsValid      bool   `json:"isvalid"`
	Address      string `json:"address"`
	ScriptPubKey string `json:"scriptPubKey"`
}

// ValidateAddress calls validateaddress.
func (c *Client) ValidateAddress(ctx context.Context, addr string) (*ValidateAddressResult, error) {
	var v ValidateAddressResult
	return &v, c.Call(ctx, "validateaddress", []any{addr}, &v)
}
