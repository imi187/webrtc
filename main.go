package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
	"github.com/pion/webrtc/v3"
	"github.com/rs/cors"
)

type playerJson struct {
	Position  [2]int `json:"position"`
	Theta     int    `json:"theta"`
	Animation int    `json:"animation"`
}

type webSockerJson struct {
	Action    string                    `json:"action"`
	ChannelId string                    `json:"channelId"`
	Data      webrtc.SessionDescription `json:"data"`
}

var (
	peerConnection map[string]*webrtc.PeerConnection
	connections    []*websocket.Conn
	dataChannels   map[string]*webrtc.DataChannel
	mu             sync.Mutex
	players        = map[string]playerJson{}
	send           bool
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	dataChannels = make(map[string]*webrtc.DataChannel)
	peerConnection = make(map[string]*webrtc.PeerConnection)
	players = make(map[string]playerJson)
	send = false
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleFrontend)
	mux.HandleFunc("/favicon.ico", handleFrontend1)
	mux.HandleFunc("/ws", handleWebSockerConnection)
	handler := cors.Default().Handler(mux)
	log.Println("Starting server on :3001")
	log.Fatal(http.ListenAndServe(":3001", handler))
}

func handleWebSockerConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println("Failed to upgrade connection:", err)
		return
	}

	mu.Lock()
	connections = append(connections, conn)
	mu.Unlock()

	defer func() {
		log.Println("Closing connection")
		err := conn.Close()
		if err != nil {
			log.Println("Error while closing connection:", err)
		}

		mu.Lock()
		for i, c := range connections {
			if c == conn {
				connections = append(connections[:i], connections[i+1:]...)
				break
			}
		}
		mu.Unlock()
	}()

	log.Println("Client connected")

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("Error while reading message:", err)
			break
		}
		log.Printf("Received message: %s\n", msg)

		var websocketJson webSockerJson
		_ = json.Unmarshal(msg, &websocketJson)

		if websocketJson.Action == "offer" {

			log.Println("Creating offer")

			offer := createOffer(conn, websocketJson.ChannelId)

			offerWebSockerData := struct {
				Action string                    `json:"action"`
				Data   webrtc.SessionDescription `json:"data"`
			}{
				Action: "offer",
				Data:   offer,
			}

			response, err := json.Marshal(offerWebSockerData)

			if err != nil {
				log.Println("Failed to marshal answer")
				return
			}
			conn.WriteMessage(1, response)
		}

		if websocketJson.Action == "answer" {
			createAnswer(websocketJson.Data, websocketJson.ChannelId)
		}

		if err != nil {
			log.Println("Error while writing message:", err)
			break
		}
	}
}

func handleFrontend1(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func handleFrontend(w http.ResponseWriter, r *http.Request) {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal(err)
	}
	host := os.Getenv("HOST")
	ws := os.Getenv("WS")
	log.Println("Reading file")
	fileName := "index.html"
	stringBytes, err := os.ReadFile(fileName)
	if err != nil {
		panic(err)
	}
	htmlString := string(stringBytes[:])
	htmlString = strings.Replace(htmlString, "{host}", host, 3)
	htmlString = strings.Replace(htmlString, "{ws}", ws, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(htmlString))
}

func createAnswer(data webrtc.SessionDescription, ChannelId string) {

	sdp := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: data.SDP}
	if err := peerConnection[ChannelId].SetRemoteDescription(sdp); err != nil {
		log.Printf("error setting remote description: %s\n", err)
		return
	}
}

func createOffer(conn *websocket.Conn, channelId string) webrtc.SessionDescription {

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:global.stun.twilio.com:3478"},
			},
			{
				Username:   "dc2d2894d5a9023620c467b0e71cfa6a35457e6679785ed6ae9856fe5bdfa269",
				Credential: "tE2DajzSJwnsSbc123",
				URLs:       []string{"turn:global.turn.twilio.com:3478?transport=udp"},
			},
			{
				Username:   "dc2d2894d5a9023620c467b0e71cfa6a35457e6679785ed6ae9856fe5bdfa269",
				Credential: "tE2DajzSJwnsSbc123",
				URLs:       []string{"turn:global.turn.twilio.com:3478?transport=tcp"},
			},
			{
				Username:   "dc2d2894d5a9023620c467b0e71cfa6a35457e6679785ed6ae9856fe5bdfa269",
				Credential: "tE2DajzSJwnsSbc123",
				URLs:       []string{"turn:global.turn.twilio.com:443?transport=tcp"},
			},
		},
	}

	mu.Lock()
	var err error
	peerConnection[channelId], err = webrtc.NewPeerConnection(config)
	if err != nil {
		//return
	}
	mu.Unlock()

	peerConnection[channelId].OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State has changed: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateDisconnected {
			mu.Lock()
			delete(dataChannels, channelId)
			delete(players, channelId)
			delete(peerConnection, channelId)
			mu.Unlock()
			log.Println("Channel deleted")
		}
	})

	dataChannel, err := peerConnection[channelId].CreateDataChannel(channelId, nil)

	if err != nil {
		log.Fatal(err)
	}

	peerConnection[channelId].OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			candidateJSON := c.ToJSON()

			type dataStruct struct {
				Candidate     string `json:"candidate"`
				SDPMid        string `json:"sdpMid"`
				SDPMLineIndex int    `json:"sdpMLineIndex"`
			}

			var candidate dataStruct
			candidate.Candidate = candidateJSON.Candidate
			candidate.SDPMid = derefString(candidateJSON.SDPMid)
			candidate.SDPMLineIndex = derefUint16(candidateJSON.SDPMLineIndex)

			candidateToSend := struct {
				Action string     `json:"action"`
				Data   dataStruct `json:"data"`
			}{
				Action: "candidate",
				Data:   candidate,
			}

			json, _ := json.Marshal(candidateToSend)
			log.Printf("%s", json)
			_ = conn.WriteMessage(1, json)

		}
	})

	dataChannels[channelId] = dataChannel
	coordinates := [2]int{0, 0}

	var playerVar playerJson
	playerVar.Position = coordinates
	playerVar.Theta = 0
	playerVar.Animation = 2

	players[channelId] = playerVar

	dataChannel.OnOpen(func() {
		log.Printf("On %s\n", "Open")
	})

	dataChannel.OnClose(func() {
		delete(dataChannels, channelId)
		delete(peerConnection, channelId)
		log.Println("Channel close")
	})

	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		var playerJson playerJson
		_ = json.Unmarshal(msg.Data, &playerJson)
		players[channelId] = playerJson
		send = true
		broadcastMessage()
	})

	offer, err := peerConnection[channelId].CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	err = peerConnection[channelId].SetLocalDescription(offer)
	if err != nil {
		panic(err)
	}

	return offer
}

func broadcastMessage() {
	i := 0
	jsonData, _ := json.Marshal(players)
	log.Println(string(jsonData))
	for _, channel := range dataChannels {
		log.Printf("ID-NUMBER: %d", i)
		if err := channel.SendText(string(jsonData)); err != nil {
			log.Printf("Failed to send message on data channel '%s': %s", channel.Label(), err)
		}
		i++
	}
}

func derefString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func derefUint16(ptr *uint16) int {
	if ptr == nil {
		return 0
	}
	return int(*ptr)
}

/*
func handleConnection(w http.ResponseWriter, r *http.Request) {
	websocketConnection, _ = upgrader.Upgrade(w, r, nil)
	defer func() {
		log.Println("Closing connection")
		err := websocketConnection.Close()
		if err != nil {
			log.Println("Error while closing connection:", err)
		}
	}()
	log.Println("Client connected")
	for {
		messageType, msg, err := websocketConnection.ReadMessage()
		if err != nil {
			log.Println("Error while reading message:", err)
			break
		}
		log.Printf("Received message: %s\n", msg)

		err = websocketConnection.WriteMessage(messageType, msg)
		if err != nil {
			log.Println("Error while writing message:", err)
			break
		}
	}
}
*/

/*

func handleAnswer(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var answer struct {
		ChannelId string `json:"channelId"`
		Answer    struct {
			Type string `json:"type"`
			SDP  string `json:"sdp"`
		}
	}

	if err := json.NewDecoder(r.Body).Decode(&answer); err != nil {
		http.Error(w, "Failed to decode offer", http.StatusBadRequest)
		return
	}

	if answer.Answer.Type != "answer" || answer.Answer.SDP == "" {
		http.Error(w, "Invalid offer", http.StatusBadRequest)
		return
	}

	sdp := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.Answer.SDP}
	if err := peerConnection[answer.ChannelId].SetRemoteDescription(sdp); err != nil {
		log.Printf("error setting remote description: %s\n", err)
		http.Error(w, "Failed to set remote description", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}*/

/*

func handleOffer(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var postBody struct {
		ChannelId string `json:"channelId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&postBody); err != nil {
		http.Error(w, "Failed to decode postBody", http.StatusBadRequest)
		return
	}

	if postBody.ChannelId == "" {
		http.Error(w, "Invalid ChannelId", http.StatusBadRequest)
		return
	}

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:global.stun.twilio.com:3478"},
			},
			{
				Username:   "dc2d2894d5a9023620c467b0e71cfa6a35457e6679785ed6ae9856fe5bdfa269",
				Credential: "tE2DajzSJwnsSbc123",
				URLs:       []string{"turn:global.turn.twilio.com:3478?transport=udp"},
			},
			{
				Username:   "dc2d2894d5a9023620c467b0e71cfa6a35457e6679785ed6ae9856fe5bdfa269",
				Credential: "tE2DajzSJwnsSbc123",
				URLs:       []string{"turn:global.turn.twilio.com:3478?transport=tcp"},
			},
			{
				Username:   "dc2d2894d5a9023620c467b0e71cfa6a35457e6679785ed6ae9856fe5bdfa269",
				Credential: "tE2DajzSJwnsSbc123",
				URLs:       []string{"turn:global.turn.twilio.com:443?transport=tcp"},
			},
		},
	}

	mu.Lock()
	var err error
	peerConnection[postBody.ChannelId], err = webrtc.NewPeerConnection(config)
	if err != nil {
		return
	}
	mu.Unlock()

	peerConnection[postBody.ChannelId].OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State has changed: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateDisconnected {
			mu.Lock()
			delete(dataChannels, postBody.ChannelId)
			delete(players, postBody.ChannelId)
			delete(peerConnection, postBody.ChannelId)
			mu.Unlock()
			log.Println("Channel deleted")
		}
	})

	dataChannel, err := peerConnection[postBody.ChannelId].CreateDataChannel("test", nil)

	if err != nil {
		log.Fatal(err)
	}

	peerConnection[postBody.ChannelId].OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			candidateJSON := c.ToJSON()
			candidate := struct {
				Candidate     string `json:"candidate"`
				SDPMid        string `json:"sdpMid"`
				SDPMLineIndex int    `json:"sdpMLineIndex"`
			}{
				Candidate:     candidateJSON.Candidate,
				SDPMid:        derefString(candidateJSON.SDPMid),
				SDPMLineIndex: derefUint16(candidateJSON.SDPMLineIndex),
			}
			json, _ := json.Marshal(candidate)
			log.Printf("%s", json)
			_ = websocketConnection.WriteMessage(1, json)
		}
	})

	dataChannels[postBody.ChannelId] = dataChannel
	coordinates := [2]int{0, 0}

	var playerVar playerJson
	playerVar.Position = coordinates
	playerVar.Theta = 0
	playerVar.Animation = 2

	players[postBody.ChannelId] = playerVar

	dataChannel.OnOpen(func() {
		log.Printf("On %s\n", "Open")
	})

	dataChannel.OnClose(func() {
		delete(dataChannels, postBody.ChannelId)
		delete(peerConnection, postBody.ChannelId)
		log.Println("Channel close")
	})

	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		var playerJson playerJson
		_ = json.Unmarshal(msg.Data, &playerJson)
		players[postBody.ChannelId] = playerJson
		broadcastMessage()
	})

	offer, err := peerConnection[postBody.ChannelId].CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	err = peerConnection[postBody.ChannelId].SetLocalDescription(offer)
	if err != nil {
		panic(err)
	}

	response, err := json.Marshal(offer)
	if err != nil {
		http.Error(w, "Failed to marshal answer", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(response)
}*************************************************************/
