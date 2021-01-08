package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

var wsUpgrade = websocket.Upgrader{}

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
