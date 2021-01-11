package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/lemon-mint/godotenv"
)

var syncServers []string
var _ = func() int {
	godotenv.Load()
	if os.Getenv("EVENTBROKER_SYNC") == "on" {
		i := 0
		for {
			serverURI := os.Getenv("EVENTBROKER_SYNC_SERVER_" + strconv.Itoa(i))
			if serverURI != "" {
				syncServers = append(syncServers, serverURI)
			} else {
				break
			}
			i++
		}
	}
	return 0
}()
var syncConnetions = []*websocket.Conn{}
var hmacCounter = 0
var nodeID = randURLSafe(32)

var syncLock sync.Mutex

var wsUpgrade = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var pushCH = make(map[string]map[string]chan msg)
var chlock sync.Mutex

func send(ch string, data msg) {
	chlock.Lock()
	defer chlock.Unlock()
	_, ok := pushCH[ch]
	if ok {
		for key := range pushCH[ch] {
			data.SessionID = key
			pushCH[ch][key] <- data
		}
	}
}

func getCH(ch string) (string, chan msg) {
	chlock.Lock()
	defer chlock.Unlock()
	NewCH := make(chan msg, 10)
	_, ok := pushCH[ch]
	if !ok {
		pushCH[ch] = make(map[string]chan msg)
	}
	sessionID := randURLSafe(16)
	pushCH[ch][sessionID] = NewCH
	return sessionID, NewCH
}

func delCH(ch string, sessionID string) {
	chlock.Lock()
	defer chlock.Unlock()
	delete(pushCH[ch], sessionID)
}

type msg struct {
	PacketType string `json:"type"` // heartbeat or event
	TimeStamp  int    `json:"timestamp"`
	SessionID  string `json:"session"`
	EventID    string `json:"id"`
	EventType  string `json:"event"` // 0 < len(msg.EventType) < 32
}

func randURLSafe(size int) string {
	buf := make([]byte, size)
	rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func eventlistener(c echo.Context) error {
	ch := c.Param("ch")
	ws, err := wsUpgrade.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}

	sessionID, pipe := getCH(ch)
	defer delCH(ch, sessionID)

	defer ws.Close()

	var HeartBeatLock sync.Mutex
	var HeartBeatState bool = true

	ws.SetCloseHandler(func(code int, text string) error {
		HeartBeatLock.Lock()
		HeartBeatState = false
		HeartBeatLock.Unlock()
		close(pipe)
		return nil
	})

	defer func() {
		HeartBeatLock.Lock()
		HeartBeatState = false
		HeartBeatLock.Unlock()
	}()

	go func() {
		for {
			HeartBeatLock.Lock()
			state := HeartBeatState
			if state {
				select {
				case pipe <- msg{
					PacketType: "heartbeat",
				}:
				default:
					//Chan is full
				}
			} else {
				return
			}
			HeartBeatLock.Unlock()
			time.Sleep(time.Second * 15)
		}
	}()

	for {
		value, ok := <-pipe
		if ok {
			err := ws.WriteJSON(value)
			if err != nil {
				fmt.Println(err)
				c.Logger().Error(err)
				break
			}
		} else {
			break
		}
	}
	return nil
}

var counter = make(map[string]int)

type syncmsg struct {
	MsgType   string `json:"msgtype"`
	NodeID    string `json:"nodeid"`
	TimeStamp int    `json:"ts"`
	Counter   int    `json:"counter"`
	Ch        string `json:"ch"`
	Type      string `json:"type"`
	Nonce     string `json:"nonce"`
	EventID   string `json:"id"`
	HMAC      string `json:"hmac"`
}

func syncPoint(c echo.Context) error {
	ws, err := wsUpgrade.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}

	defer ws.Close()
	for {
		data := new(syncmsg)
		err = ws.ReadJSON(data)
		if err != nil {
			fmt.Println(err)
			c.Logger().Error(err)
			break
		}
		if data.MsgType == "event" {
			signer := hmac.New(sha512.New384, []byte(os.Getenv("EVENTBROKER_SECRET_KEY")))
			signer.Write(
				[]byte(
					data.NodeID + strconv.Itoa(data.TimeStamp) + strconv.Itoa(data.Counter) + data.Ch + data.Type + data.Nonce + data.EventID,
				),
			)
			checkSum, err := base64.RawURLEncoding.DecodeString(data.HMAC)
			if err != nil {
				fmt.Println(err)
				c.Logger().Error(err)
				break
			}
			mac := signer.Sum(nil)
			if bytes.Equal(mac, checkSum) {
				prevCounter, ok := counter[data.NodeID]
				if !ok {
					counter[data.NodeID] = data.TimeStamp
				}
				if data.Counter > prevCounter {
					counter[data.NodeID] = data.Counter
					dataMsg := msg{
						PacketType: "event",
						TimeStamp:  data.TimeStamp,
						EventID:    data.EventID,
						EventType:  data.Type,
					}
					go send(data.Ch, dataMsg)
				}
			}
		}
	}
	return nil
}

func sendSync(ch string, tosync msg) {
	hmacCounter++
	data := syncmsg{
		MsgType:   "event",
		NodeID:    nodeID,
		TimeStamp: tosync.TimeStamp,
		Counter:   int(time.Now().UTC().Unix()) + hmacCounter,
		Ch:        ch,
		Type:      tosync.EventType,
		Nonce:     randURLSafe(16),
		EventID:   tosync.EventID,
	}
	signer := hmac.New(sha512.New384, []byte(os.Getenv("EVENTBROKER_SECRET_KEY")))
	signer.Write(
		[]byte(
			data.NodeID + strconv.Itoa(data.TimeStamp) + strconv.Itoa(data.Counter) + data.Ch + data.Type + data.Nonce + data.EventID,
		),
	)
	data.HMAC = base64.RawURLEncoding.EncodeToString(signer.Sum(nil))
	syncLock.Lock()
	for i := range syncConnetions {
		syncConnetions[i].WriteJSON(data)
	}
	syncLock.Unlock()
}

func syncHeartbeat() {
	for {
		data := syncmsg{
			MsgType: "heartbeat",
		}
		time.Sleep(time.Second * 15)
		syncLock.Lock()
		for i := range syncConnetions {
			syncConnetions[i].WriteJSON(data)
		}
		syncLock.Unlock()
	}
}

func deployEvent(c echo.Context) error {
	ch := c.Param("ch")
	EventType := c.QueryParams().Get("type")
	if EventType == "" {
		EventType = "default"
	}
	chHASH := sha256.Sum256([]byte(ch))
	chID := hex.EncodeToString(chHASH[:])
	data := msg{
		PacketType: "event",
		TimeStamp:  int(time.Now().UTC().Unix()),
		EventID:    randURLSafe(16),
		EventType:  EventType,
	}
	go send(chID, data)
	go sendSync(chID, data)
	return c.JSON(200, data)
}

var isReconnecting = false

func connectToSyncServer() {
	syncLock.Lock()
	for i := range syncConnetions {
		syncConnetions[i].Close()
	}
	syncConnetions = []*websocket.Conn{}
	for i := range syncServers {
		u, err := url.Parse(syncServers[i])
		if err == nil {
			c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
			if err != nil {
				fmt.Println(err)
			}
			c.SetCloseHandler(func(code int, text string) error {
				if !isReconnecting {
					isReconnecting = true
					connectToSyncServer()
					isReconnecting = false
				}
				return nil
			})
			if err == nil {
				syncConnetions = append(syncConnetions, c)
			}
		}
	}
	syncLock.Unlock()
}

func main() {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Static("/", "public")
	e.GET("/event/:ch", deployEvent)
	e.GET("/ws/:ch", eventlistener)

	e.GET("/hc", func(c echo.Context) error {
		return c.String(200, "OK")
	})

	admin := e.Group("/admin")
	admin.Use(middleware.BasicAuth(
		func(username, pw string, c echo.Context) (bool, error) {
			t := sha512.Sum384([]byte(username + pw + os.Getenv("EVENT_BROKER_ADMIN_SALT")))
			for i := 0; i < 2000; i++ {
				t = sha512.Sum384(t[:])
			}
			adminPW, err := hex.DecodeString(os.Getenv("EVENT_BROKER_ADMIN_PW"))
			if err != nil {
				return false, err
			}
			if subtle.ConstantTimeCompare(t[:], adminPW) != 1 {
				return false, nil
			}
			if username != os.Getenv("EVENT_BROKER_ADMIN_ID") {
				return false, nil
			}
			return true, nil
		},
	))
	admin.GET("/connections", func(c echo.Context) error {
		activeConnections := 0
		channels := 0
		chlock.Lock()
		for key := range pushCH {
			channels++
			activeConnections += len(pushCH[key])
		}
		chlock.Unlock()
		return c.JSONPretty(200, struct {
			Channels    int `json:"channels"`
			Connections int `json:"connections"`
		}{
			Channels:    channels,
			Connections: activeConnections,
		}, "    ")
	})

	admin.GET("/syncstates", func(c echo.Context) error {
		syncLock.Lock()
		connections := len(syncConnetions)
		syncLock.Unlock()
		return c.JSONPretty(200, struct {
			Servers     []string `json:"servers"`
			Connections int      `json:"connections"`
		}{
			Servers:     syncServers,
			Connections: connections,
		}, "    ")
	})

	if os.Getenv("EVENTBROKER_SYNC") == "on" {
		e.GET("/sync/syncpoint", syncPoint)
	}
	go func() {
		time.Sleep(time.Second * 5)
		connectToSyncServer()
		syncHeartbeat()
	}()

	if os.Getenv("PORT_FROM_ENV") != "" {
		e.Logger.Fatal(e.Start(":" + os.Getenv("PORT")))
	} else {
		e.Logger.Fatal(e.Start(":16745"))
	}
}
