package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/lemon-mint/godotenv"
)

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
	NewCH := make(chan msg)
	_, ok := pushCH[ch]
	if !ok {
		pushCH[ch] = make(map[string]chan msg)
	}
	sessionID := randURLSafe(16)
	pushCH[ch][sessionID] = NewCH
	return sessionID, NewCH
}

func delCH(ch string, sessionID string) {
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
	defer ws.Close()

	sessionID, pipe := getCH(ch)
	defer delCH(ch, sessionID)

	for {
		err := ws.WriteJSON(<-pipe)
		if err != nil {
			fmt.Println(err)
			c.Logger().Error(err)
			break
		}
	}
	return nil
}

var counter = make(map[string]int)

type syncmsg struct {
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
				chHASH := sha256.Sum256([]byte(data.Ch))
				chID := hex.EncodeToString(chHASH[:])
				data := msg{
					PacketType: "event",
					TimeStamp:  data.TimeStamp,
					EventID:    data.EventID,
					EventType:  data.Type,
				}
				go send(chID, data)
			}
		}
	}
	return nil
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
	return c.JSON(200, data)
}

func main() {
	godotenv.Load()
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Static("/", "public")
	e.GET("/event/:ch", deployEvent)
	e.GET("/ws/:ch", eventlistener)
	if os.Getenv("PORT_FROM_ENV") != "" {
		e.Logger.Fatal(e.Start(":" + os.Getenv("PORT")))
	} else {
		e.Logger.Fatal(e.Start(":16745"))
	}
}
