package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/joho/godotenv"
	"github.com/pion/webrtc/v3"
	"github.com/rs/cors"
)

var (
	peerConnection *webrtc.PeerConnection
	dataChannels   map[string]*webrtc.DataChannel
	mu             sync.Mutex
	coordinates    = [2]int{0, 0}
)

func main() {
	dataChannels = make(map[string]*webrtc.DataChannel)
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleFrontend)
	mux.HandleFunc("/offer", handleOffer)
	mux.HandleFunc("/ice", handleICE)
	handler := cors.Default().Handler(mux)
	log.Println("Starting server on :3001")
	log.Fatal(http.ListenAndServe(":3001", handler))
}

func handleFrontend(w http.ResponseWriter, r *http.Request) {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal(err)
	}
	host := os.Getenv("HOST")
	fmt.Println("Reading file")
	fileName := "index.html"
	stringBytes, err := os.ReadFile(fileName)
	if err != nil {
		panic(err)
	}
	htmlString := string(stringBytes[:])
	htmlString = strings.Replace(htmlString, "{host}", host, 2)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(htmlString))
}

func handleOffer(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var offer struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}
	if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
		http.Error(w, "Failed to decode offer", http.StatusBadRequest)
		return
	}

	if offer.Type != "offer" || offer.SDP == "" {
		http.Error(w, "Invalid offer", http.StatusBadRequest)
		return
	}

	api := webrtc.NewAPI(webrtc.WithSettingEngine(webrtc.SettingEngine{}))

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:5349"},
			},
		},
	}

	var err error
	peerConnection, err = api.NewPeerConnection(config)
	if err != nil {
		http.Error(w, "Failed to create PeerConnection", http.StatusInternalServerError)
		return
	}

	peerConnection.OnDataChannel(func(d *webrtc.DataChannel) {

		addressStr := fmt.Sprintf("%p", d.ID())

		peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
			log.Printf("ICE Connection State has changed: %s\n", connectionState.String())
			if connectionState == webrtc.ICEConnectionStateDisconnected {
				// Verwerking van het sluiten
				mu.Lock()
				delete(dataChannels, addressStr)
				mu.Unlock()
				fmt.Println("Channel deleted")
			}
		})

		log.Printf("Data channel '%s' received\n", d.Label())

		d.OnOpen(func() {
			log.Printf("Connection Open")
		})

		d.OnClose(func() {

			mu.Lock()
			delete(dataChannels, addressStr)
			mu.Unlock()
			fmt.Println("Channel deleted")
		})

		mu.Lock()
		dataChannels[addressStr] = d
		mu.Unlock()

		d.OnMessage(func(msg webrtc.DataChannelMessage) {

			var direction = string(msg.Data)
			var arr []int
			_ = json.Unmarshal(msg.Data, &arr)
			coordinates = [2]int(arr)
			log.Printf("Message received from data channel '%s': %s\n", d.Label(), direction)
			broadcastMessage()
		})
	})

	sdp := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}
	if err := peerConnection.SetRemoteDescription(sdp); err != nil {
		log.Printf("error setting remote description: %s\n", err)
		http.Error(w, "Failed to set remote description", http.StatusInternalServerError)
		return
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		http.Error(w, "Failed to create answer", http.StatusInternalServerError)
		return
	}

	if err := peerConnection.SetLocalDescription(answer); err != nil {
		http.Error(w, "Failed to set local description", http.StatusInternalServerError)
		return
	}

	response, err := json.Marshal(answer)
	if err != nil {
		http.Error(w, "Failed to marshal answer", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(response)
}

func handleICE(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	var candidate struct {
		Candidate     string `json:"candidate"`
		SDPMid        string `json:"sdpMid"`
		SDPMLineIndex int    `json:"sdpMLineIndex"`
	}

	if err := json.NewDecoder(r.Body).Decode(&candidate); err != nil {
		http.Error(w, "Failed to decode ICE candidate", http.StatusBadRequest)
		return
	}

	iceCandidate := webrtc.ICECandidateInit{
		Candidate:     candidate.Candidate,
		SDPMid:        strPtr(candidate.SDPMid),
		SDPMLineIndex: uint16Ptr(uint16(candidate.SDPMLineIndex)),
	}

	if err := peerConnection.AddICECandidate(iceCandidate); err != nil {
		log.Printf("error adding ICE candidate: %s\n", err)
		http.Error(w, "Failed to add ICE candidate", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func broadcastMessage() {
	//mu.Lock()
	//defer mu.Unlock()
	i := 0
	for _, channel := range dataChannels {
		coorJson, _ := json.Marshal(coordinates)
		log.Printf("ID-NUMBER: %d", i)
		if err := channel.SendText(string(coorJson)); err != nil {
			log.Printf("Failed to send message on data channel '%s': %s", channel.Label(), err)
		}
		i++
	}
}

func strPtr(s string) *string {
	return &s
}

func uint16Ptr(i uint16) *uint16 {
	return &i
}
