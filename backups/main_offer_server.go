package main

import (
	"encoding/json"
	"fmt"
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

var (
	peerConnection      *webrtc.PeerConnection
	websocketConnection *websocket.Conn
	dataChannels        map[string]*webrtc.DataChannel
	mu                  sync.Mutex
	coordinates         = [2]int{0, 0}
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func main() {
	dataChannels = make(map[string]*webrtc.DataChannel)
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleFrontend)
	mux.HandleFunc("/favicon.ico", handleFrontend1)
	mux.HandleFunc("/offer", handleOffer)
	mux.HandleFunc("/answer", handleAnswer)
	mux.HandleFunc("/ws", handleConnection)
	handler := cors.Default().Handler(mux)
	log.Println("Starting server on :3001")
	log.Fatal(http.ListenAndServe(":3001", handler))
}

func handleConnection(w http.ResponseWriter, r *http.Request) {
	websocketConnection, _ = upgrader.Upgrade(w, r, nil)
	defer func() {
		fmt.Println("Closing connection")
		err := websocketConnection.Close()
		if err != nil {
			fmt.Println("Error while closing connection:", err)
		}
	}()
	fmt.Println("Client connected")
	for {
		messageType, msg, err := websocketConnection.ReadMessage()
		if err != nil {
			fmt.Println("Error while reading message:", err)
			break
		}
		fmt.Printf("Received message: %s\n", msg)

		err = websocketConnection.WriteMessage(messageType, msg)
		if err != nil {
			fmt.Println("Error while writing message:", err)
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
	fmt.Println("Reading file")
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

func handleAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var answer struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&answer); err != nil {
		http.Error(w, "Failed to decode offer", http.StatusBadRequest)
		return
	}

	if answer.Type != "answer" || answer.SDP == "" {
		http.Error(w, "Invalid offer", http.StatusBadRequest)
		return
	}

	sdp := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}
	if err := peerConnection.SetRemoteDescription(sdp); err != nil {
		log.Printf("error setting remote description: %s\n", err)
		http.Error(w, "Failed to set remote description", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

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

	fmt.Print(postBody.ChannelId)

	if postBody.ChannelId == "" {
		http.Error(w, "Invalid ChannelId", http.StatusBadRequest)
		return
	}

	config := webrtc.Configuration{
		//ICETransportPolicy: webrtc.ICETransportPolicyRelay,
		ICEServers: []webrtc.ICEServer{
			{
				URLs:       []string{"turn:turn.bistri.com:80"},
				Username:   "homeo",
				Credential: "homeo",
			},
		},
	}

	mu.Lock()
	var err error
	peerConnection, err = webrtc.NewPeerConnection(config)
	if err != nil {
		return
	}
	mu.Unlock()

	dataChannel, err := peerConnection.CreateDataChannel("test", nil)

	if err != nil {
		log.Fatal(err)
	}

	peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
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
			fmt.Printf("%s", json)
			_ = websocketConnection.WriteMessage(1, json)
		}
	})

	dataChannel.OnOpen(func() {
		addressStr := fmt.Sprintf("%p", dataChannel.ID())
		fmt.Println(addressStr)
		dataChannels[addressStr] = dataChannel
		fmt.Printf("On %s\n", "Open")
	})

	dataChannel.OnClose(func() {
		addressStr := fmt.Sprintf("%p", dataChannel.ID())
		fmt.Println(addressStr)
		delete(dataChannels, addressStr)
		fmt.Println("Channel close")
	})

	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		var direction = string(msg.Data)
		var arr []int
		_ = json.Unmarshal(msg.Data, &arr)
		coordinates = [2]int(arr)
		log.Printf("Message received from data channel '%s': %s\n", dataChannel.Label(), direction)
		broadcastMessage()
	})

	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	err = peerConnection.SetLocalDescription(offer)
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
}

func broadcastMessage() {
	i := 0
	coorJson, _ := json.Marshal(coordinates)
	for _, channel := range dataChannels {
		log.Printf("ID-NUMBER: %d", i)
		if err := channel.SendText(string(coorJson)); err != nil {
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
