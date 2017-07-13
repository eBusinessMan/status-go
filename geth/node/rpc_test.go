package node_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/status-im/status-go/geth/node"
	"github.com/status-im/status-go/geth/params"
	"github.com/status-im/status-go/geth/proxy"
	. "github.com/status-im/status-go/geth/testing"
	"github.com/stretchr/testify/suite"
)

type service struct {
	Handler http.HandlerFunc
}

func (s service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.Handler(w, r)
}

func TestRPCTestSuite(t *testing.T) {
	suite.Run(t, new(RPCTestSuite))
}

type RPCTestSuite struct {
	BaseTestSuite
}

func (s *RPCTestSuite) SetupTest() {
	require := s.Require()

	s.NodeManager = proxy.NewRPCRouter(node.NewNodeManager())
	require.NotNil(s.NodeManager)
	require.IsType(&proxy.RPCRouter{}, s.NodeManager)
}

func (s *RPCTestSuite) TestUpstreamCallRPC() {
	require := s.Require()
	require.NotNil(s.NodeManager)

	expectedResponse := `{"jsonrpc": "2.0", "status":200, "result": "3434=done"}`

	nodeConfig, err := MakeTestNodeConfig(params.RinkebyNetworkID)
	require.NoError(err)

	rpcService := service{Handler: func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		var req map[string]interface{}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			require.NoError(err)
			return
		}

		method, ok := req["method"]
		require.NotEqual(ok, false)
		require.IsType((string)(""), method)
		require.Equal(method, "eth_swapspace")

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(expectedResponse))
	}}

	httpRPCServer := httptest.NewServer(rpcService)

	nodeConfig.UpstreamConfig.Enabled = true
	nodeConfig.UpstreamConfig.URL = httpRPCServer.URL

	started, err := s.NodeManager.StartNode(nodeConfig)
	require.NoError(err)

	// Attempt to find out if we started well.
	select {
	case <-started:
		break
	case <-time.After(1 * time.Second):
		s.T().Fatal("failed to start node manager")
		break
	}

	defer s.NodeManager.StopNode()

	rpcClient := node.NewRPCManager(s.NodeManager)
	require.NotNil(rpcClient)

	responseString := rpcClient.Call(`{
		"jsonrpc": "2.0",
		"method": "eth_swapspace",
		"args": [1,50,1]
	}`)

	require.Equal(expectedResponse, responseString)
}

func (s *RPCTestSuite) TestCallRPC() {
	require := s.Require()
	require.NotNil(s.NodeManager)

	rpcClient := node.NewRPCManager(s.NodeManager)
	require.NotNil(rpcClient)

	nodeConfig, err := MakeTestNodeConfig(params.RinkebyNetworkID)
	require.NoError(err)

	nodeConfig.IPCEnabled = false
	nodeConfig.WSEnabled = false
	nodeConfig.HTTPHost = "" // to make sure that no HTTP interface is started
	nodeStarted, err := s.NodeManager.StartNode(nodeConfig)
	require.NoError(err)
	require.NotNil(nodeConfig)
	defer s.NodeManager.StopNode()
	<-nodeStarted

	progress := make(chan struct{}, 25)
	type rpcCall struct {
		inputJSON string
		validator func(resultJSON string)
	}
	var rpcCalls = []rpcCall{
		{
			`{"jsonrpc":"2.0","method":"eth_sendTransaction","params":[{
				"from": "0xb60e8dd61c5d32be8058bb8eb970870f07233155",
				"to": "0xd46e8dd67c5d32be8058bb8eb970870f07244567",
				"gas": "0x76c0",
				"gasPrice": "0x9184e72a000",
				"value": "0x9184e72a",
				"data": "0xd46e8dd67c5d32be8d46e8dd67c5d32be8058bb8eb970870f072445675058bb8eb970870f072445675"}],"id":1}`,
			func(resultJSON string) {
				log.Info("eth_sendTransaction")
				s.T().Log("GOT: ", resultJSON)
				progress <- struct{}{}
			},
		},
		{
			`{"jsonrpc":"2.0","method":"shh_version","params":[],"id":67}`,
			func(resultJSON string) {
				expected := `{"jsonrpc":"2.0","id":67,"result":"0x5"}` + "\n"
				s.Equal(expected, resultJSON)
				s.T().Log("shh_version: ", resultJSON)
				progress <- struct{}{}
			},
		},
		{
			`{"jsonrpc":"2.0","method":"web3_sha3","params":["0x68656c6c6f20776f726c64"],"id":64}`,
			func(resultJSON string) {
				expected := `{"jsonrpc":"2.0","id":64,"result":"0x47173285a8d7341e5e972fc677286384f802f8ef42a5ec5f03bbfa254cb01fad"}` + "\n"
				s.Equal(expected, resultJSON)
				s.T().Log("web3_sha3: ", resultJSON)
				progress <- struct{}{}
			},
		},
		{
			`{"jsonrpc":"2.0","method":"net_version","params":[],"id":67}`,
			func(resultJSON string) {
				expected := `{"jsonrpc":"2.0","id":67,"result":"4"}` + "\n"
				s.Equal(expected, resultJSON)
				s.T().Log("net_version: ", resultJSON)
				progress <- struct{}{}
			},
		},
	}

	cnt := len(rpcCalls) - 1 // send transaction blocks up until complete/discarded/times out
	for _, r := range rpcCalls {
		go func(r rpcCall) {
			s.T().Logf("Run test: %v", r.inputJSON)
			resultJSON := rpcClient.Call(r.inputJSON)
			r.validator(resultJSON)
		}(r)
	}

	for range progress {
		cnt -= 1
		if cnt <= 0 {
			break
		}
	}
}
