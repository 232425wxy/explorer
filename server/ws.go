package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	httprpc "github.com/232425wxy/BFT/rpc/client/http"
	coretypes "github.com/232425wxy/BFT/rpc/core/types"
	"github.com/232425wxy/BFT/types"
	"github.com/232425wxy/explorer/core"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Metas []*types.BlockMeta

func (m Metas) Less(i, j int) bool {
	return m[i].Header.Height < m[j].Header.Height
}

func (m Metas) Len() int {
	return len(m)
}

func (m Metas) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}

var Websocket = &ws{
	upgrader: &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	},
}

type ws struct {
	upgrader *websocket.Upgrader
}

type Query struct {
	Msg string `json:"msg"`
	Typ string `json:"typ"`
}

func (s *ws) Handle() gin.HandlerFunc {
	return func(c *gin.Context) {
		host := c.Query("name")
		conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			panic(err)
		}

		events := make(chan core.Event)
		go func() {
			var query Query
			for {
				err := conn.ReadJSON(&query)
				if err != nil {
					return
				}
				handleMsg(query, events, host)
			}
		}()

		for {
			select {
			case event := <-events:
				if conn.WriteJSON(&event) != nil {
					return
				}
			}
		}
	}
}

func handleMsg(query Query, events chan core.Event, host string) {
	client, err := httprpc.New(host, "/websocket")
	if err != nil {
		events <- core.Event{
			Type:      core.EventTypeMsg,
			Result:      "Establish connection",
			Timestamp: time.Now().UnixNano() / 1e6,
			Text:      "Failed",
		}
		return
	}
	switch query.Typ {
	case "tx":
		res, err := client.ABCIQuery(context.Background(), query.Msg)
		if err != nil || res.Response.Log == "does not exist" {
			events <- core.Event{
				Type:      core.EventTypeMsg,
				Result:      "Query tx",
				Timestamp: time.Now().UnixNano() / 1e6,
				Text:      "Not Found",
			}
		} else {
			events <- core.Event{
				Type:      core.EventTypeMsg,
				Result:    "Query tx",
				Timestamp: time.Now().UnixNano() / 1e6,
				Text:      constructTxResult(*res),
			}
		}
	case "block":
		height, err := strconv.ParseInt(query.Msg, 10, 64)
		if err != nil {
			events <- core.Event{
				Type:      core.EventTypeMsg,
				Result:      "Query block",
				Timestamp: time.Now().UnixNano() / 1e6,
				Text:      "Not Found",
			}
			return
		}
		res, err := client.Block(context.Background(), &height)
		if err != nil {
			events <- core.Event{
				Type:      core.EventTypeMsg,
				Result:      "Query block",
				Timestamp: time.Now().UnixNano() / 1e6,
				Text:      "Not Found",
			}
		} else {
			events <- core.Event{
				Type:      core.EventTypeMsg,
				Result:    "Query block",
				Timestamp: time.Now().UnixNano() / 1e6,
				Text:      constructBlockResult(*res),
			}
		}
	case "benchmark":
		constructBenchmarkCommand(query, events)
		b := newBomber(host, broadcastTxMethod, duration, events)
		latest := latestHeight(client)
		b.start()
		time.Sleep(time.Second * 10)
		blockchainInfo(client, latest, events)
	case "stop":
		close(stop)
	case "pub":
		fmt.Println(query)
		res, err := client.BroadcastTxCommit(context.Background(), []byte(query.Msg))
		if err != nil {
			events <- core.Event{
				Type:      core.EventTypeMsg,
				Result:      "Release tx",
				Timestamp: time.Now().UnixNano() / 1e6,
				Text:      "Not Found",
			}
		} else {
			events <- core.Event{
				Type:      core.EventTypeMsg,
				Result:    "Release tx",
				Timestamp: time.Now().UnixNano() / 1e6,
				Text:      constructBroadcastResult(*res),
			}
		}
	}
}

func latestHeight(client *httprpc.HTTP) int64 {
	status, err := client.Status()
	if err != nil {
		return 0
	}
	return status.SyncInfo.LatestBlockHeight
}

func blockchainInfo(client *httprpc.HTTP, latest int64, events chan core.Event) {
	min := latest + 2
	res, err := client.BlockchainInfo(context.Background(), min, 0)
	if err != nil {
		return
	}
	metas := Metas(res.BlockMetas)
	sort.Sort(metas)
	wait := float64(metas.Len()-1) * 6.0
	interval := metas[metas.Len()-1].Header.Time.Sub(metas[0].Header.Time).Seconds()
	consensus_sum_delay := interval - wait
	consensus_delay := consensus_sum_delay / float64(metas.Len()-1)
	totalTx := 0.0
	for _, m := range metas {
		totalTx += float64(m.NumTxs)
		fmt.Println(m.Header.Time, m.NumTxs)
	}
	throughput := totalTx / consensus_sum_delay
	type s struct {
		Delay float64 `json:"delay"`
		Throughput float64 `json:"throughput"`
	}
	bz, _ := json.Marshal(s{
		Delay:      consensus_delay,
		Throughput: throughput,
	})
	buf := bytes.NewBuffer(bz)
	events <- core.Event{
		Type:      core.EventTypeMsg,
		Result:    "Benchmark result",
		Timestamp: time.Now().UnixNano() / 1e6,
		Text:      buf.String(),
	}
}

func constructBroadcastResult(res coretypes.ResultBroadcastTxCommit) string {
	type s struct {
		ABCIType string`json:"abci_type"`
		Height int64 `json:"height"`
		Hash string `json:"hash"`
	}
	bz, err := json.Marshal(s{
		ABCIType: res.DeliverTx.Events[0].Type,
		Height: res.Height,
		Hash: res.Hash.String(),
	})
	if err != nil {
		return "Marshal Failed"
	}
	buf := bytes.NewBuffer(bz)
	return buf.String()
}

func constructBenchmarkCommand(query Query, events chan core.Event) {
	rawCommand := query.Msg
	elems := strings.Split(rawCommand, ",")
	for _, e := range elems {
		kv := strings.Split(e, "=")
		if len(kv) != 2 {
			continue
		}
		if kv[0] == "method" {
			broadcastTxMethod = "broadcast_tx_" + kv[1]
		}
		if kv[0] == "duration" {
			d, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				continue
			}
			duration = int(d)
		}
		if kv[0] == "host" {
			host = kv[1]
		}
	}
}

func constructBlockResult(query coretypes.ResultBlock) string {
	type s struct {
		ChainID string `json:"chain_id"`
		Time time.Time `json:"time"`
		Height int64 `json:"height"`
		Data string `json:"data"`
		Primary string `json:"primary"`
	}
	bz, err := json.Marshal(s{
		ChainID: query.Block.ChainID,
		Time:    query.Block.Time,
		Height:  query.Block.Height,
		Data:    query.Block.Data.StringIndented(" "),
		Primary: query.Block.ProposerAddress.String(),
	})
	if err != nil {
		return "Marshal Failed"
	}
	buf := bytes.NewBuffer(bz)
	return buf.String()

}

func constructTxResult(query coretypes.ResultABCIQuery) string {
	type s struct {
		Log string `json:"log"`
		Key string `json:"key"`
		Value string `json:"value"`
		Height int64 `json:"height"`
		Index int64 `json:"index"`
	}

	bz, err := json.Marshal(s{
		Log: query.Response.Log,
		Key: string(query.Response.Key),
		Value: string(query.Response.Value),
		Height: query.Response.Height,
		Index: query.Response.Index,
	})
	if err != nil {
		return "Marshal Failed"
	}
	buf := bytes.NewBuffer(bz)
	//return fmt.Sprintf("{Log:%s	Key:%v	Value:%v	Index:%d	Height:%d	Encode:%s}", query.Response.Log, query.Response.Key, query.Response.Value,
	//	query.Response.Index, query.Response.Height, query.Response.Encode)
	return buf.String()
}
