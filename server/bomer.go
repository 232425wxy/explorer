package server

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/232425wxy/BFT/crypto/srhash"
	srjson "github.com/232425wxy/BFT/libs/json"
	srrand "github.com/232425wxy/BFT/libs/rand"
	srtime "github.com/232425wxy/BFT/libs/time"
	ctypes "github.com/232425wxy/BFT/rpc/core/types"
	rpctypes "github.com/232425wxy/BFT/rpc/jsonrpc/types"
	"github.com/232425wxy/explorer/core"
	"github.com/gorilla/websocket"
	"net/url"
	"strings"
	"time"
)

var broadcastTxMethod string = "broadcast_tx_commit"
var duration int = 10
var host string = "localhost:36657"
var stop = make(chan struct{})

type bomber struct {
	host              string
	broadcastTxMethod string
	c                 *websocket.Conn
	quit              chan struct{}
	recv              chan []byte
	duration          int
	hasSentTxNum      int
	events            chan core.Event
}

func newBomber(host, broadcastTxMethod string, duration int, events chan core.Event) *bomber {
	return &bomber{
		host:              host,
		broadcastTxMethod: broadcastTxMethod,
		quit:              make(chan struct{}),
		recv:              make(chan []byte),
		duration:          duration,
		hasSentTxNum:      0,
		events:            events,
	}
}

func (bom *bomber) start() {
	u := url.URL{Scheme: "ws", Host: bom.host, Path: "/websocket"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return
	}
	bom.c = c
	go bom.sendRoutine()
	go bom.receiveRoutine()

	stop = make(chan struct{})

	<-stop

	_ = bom.c.Close()
}

func (bom *bomber) sendRoutine() {
	random := srrand.NewRand()
	random.Seed(time.Now().UnixNano())
	clock := time.NewTimer(time.Duration(bom.duration) * time.Second)
	for {
		select {
		case <-bom.quit:
			return
		case <-clock.C:
			close(stop)
		default:
			tx, raw := generateTx(bom.hasSentTxNum+1, random)
			err := bom.c.SetWriteDeadline(srtime.Now().Add(time.Second * 30))
			if err != nil {
				fmt.Println(err)
			}
			err = bom.c.WriteJSON(rpctypes.RPCRequest{
				JSONRPC: "2.0",
				Method:  bom.broadcastTxMethod,
				Params:  tx,
				ID: rpctypes.JSONRPCStringID("BFT"),
			})
			if err != nil {
				fmt.Println(err)
			}
			recv := <-bom.recv
			//time.Sleep(100 * time.Millisecond)
			switch broadcastTxMethod {
			case "broadcast_tx_async":
				if bytes.Equal(recv, srhash.Sum(raw)) {
					bom.hasSentTxNum++
				} else {
					fmt.Println("receive wrong feedback", "expected", srhash.Sum(raw), "actual", string(recv))
				}
			case "broadcast_tx_sync":
				if bytes.Equal(recv, srhash.Sum(raw)) {
					bom.hasSentTxNum++
				} else {
					fmt.Println("receive wrong feedback", "expected", srhash.Sum(raw), "actual", string(recv))
				}
			case "broadcast_tx_commit":
				if bytes.Equal(recv, raw) {
					bom.hasSentTxNum++
					fmt.Println("new block was committed successfully")
				} else {
					fmt.Println("receive wrong feedback", "expected", srhash.Sum(raw), "actual", srhash.Sum(recv))
				}
			}

		}
	}
}

func (bom *bomber) receiveRoutine() {
	for {
		select {
		case <-bom.quit:
			return
		default:
			err := bom.c.SetReadDeadline(srtime.Now().Add(time.Second * 30))
			if err != nil {
				fmt.Println("set read deadline failed", err)
			}
			mType, p, err := bom.c.ReadMessage()
			if err != nil {
				if strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
				close(stop)
				return
			}
			switch mType {
			case websocket.TextMessage:
				var rpcRes rpctypes.RPCResponse
				err = json.Unmarshal(p, &rpcRes)
				if err != nil {
					fmt.Println("unmarshal RPCResponse failed", err)
				}
				switch broadcastTxMethod {
				case "broadcast_tx_async", "broadcast_tx_sync":
					var broadcastTx ctypes.ResultBroadcastTx
					err = srjson.Unmarshal(rpcRes.Result, &broadcastTx)
					if err != nil {
						fmt.Println("unmarshal RPCResponse.Result failed", err)
					}
					bom.recv <- broadcastTx.Hash

				case "broadcast_tx_commit":
					var broadcastTxCommit ctypes.ResultBroadcastTxCommit
					err = srjson.Unmarshal(rpcRes.Result, &broadcastTxCommit)
					if err != nil {
						fmt.Println("unmarshal RPCResponse.Result failed", err)
					}
					type s struct {
						ABCIType string `json:"abci_type"`
						Height int64 `json:"height"`
						Hash string `json:"hash"`
					}
					bz, _ := json.Marshal(s{
						ABCIType: broadcastTxCommit.DeliverTx.Events[0].Type,
						Height: broadcastTxCommit.Height,
						Hash: broadcastTxCommit.Hash.String(),
					})
					buf := bytes.NewBuffer(bz)
					bom.events <- core.Event{
						Type:      core.EventTypeMsg,
						Result:    "Benchmark",
						Timestamp: time.Now().UnixNano() / 1e6,
						Text:      buf.String(),
					}
					bom.recv <- broadcastTxCommit.DeliverTx.Events[0].Attributes[1].Value
				case "abci_query":
					var res ctypes.ResultABCIQuery
					err = srjson.Unmarshal(rpcRes.Result, &res)
					if err != nil {
						fmt.Println("unmarshal RPCResponse.Result failed", err)
					}
					fmt.Println(res.Response)
				}
			}
		}
	}
}

func generateTx(txNumber int, random *srrand.Rand) (json.RawMessage, []byte) {
	tx := make([]byte, 16)
	binary.PutUvarint(tx[:8], uint64(txNumber))
	binary.PutUvarint(tx[8:16], uint64(srtime.Now().Unix()))
	txHex := make([]byte, len(tx)*2)
	hex.Encode(txHex, tx)
	paramsJSON, err := json.Marshal(map[string]interface{}{"tx": txHex})
	if err != nil {
		fmt.Println("marshal tx failed", err)
	}
	return paramsJSON, txHex
}
