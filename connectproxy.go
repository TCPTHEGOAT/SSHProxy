package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	loggedIPs    = map[string]bool{}
	forwardCounts = map[string]int{}
	loggedForwarding = map[string]bool{}
	mu            sync.Mutex
	webhookURL    string = "WEBHOOK_URL" 
)

type DiscordEmbed struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Color       int    `json:"color"`
	Timestamp   string `json:"timestamp"`
	Fields      []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"fields"`
}

type DiscordWebhookPayload struct {
	Username string       `json:"username"`
	Content  string       `json:"content"`
	Embeds   []DiscordEmbed `json:"embeds"`
}

type DiscordError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type DiscordEmbedField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func sendDiscordEmbed(title, description string, color int, fields ...*DiscordEmbedField) error {
	if webhookURL == "" {
		return fmt.Errorf("empty or malformed webhookURL")
	}

	embed := DiscordEmbed{
		Title:       title,
		Description: description,
		Color:       color,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
	data, err := json.Marshal(embed)
	if err != nil {
		log.Printf("Failed to marshal Discord embed: %v", err)
		return err
	}

	payload := DiscordWebhookPayload{
		Username: "ANYTHING",
		Content:  "",
		Embeds:   []DiscordEmbed{embed},
	}
	data, err = json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal Discord webhook payload: %v", err)
		return err
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Failed to create HTTP request: %v", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Failed to send Discord embed: %v", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		log.Printf("Failed to send Discord embed: %s", resp.Status)
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Failed to read response body: %v", err)
			return err
		}
		log.Printf("Response Body: %s", string(body))
		var discordError DiscordError
		err = json.Unmarshal(body, &discordError)
		if err != nil {
			log.Printf("Failed to unmarshal Discord error: %v", err)
			return err
		}
		log.Printf("Discord Error: %s", discordError.Message)
		return fmt.Errorf("unexpected response status: %s", resp.Status)
	}

	return nil
}

func forward(src, dest net.Conn, direction string, wg *sync.WaitGroup) {
	defer wg.Done()
	clientIP := src.RemoteAddr().String()
	ip := clientIP[:strings.Index(clientIP, ":")]
	bytesCopied, err := io.Copy(src, dest)
	if err != nil {
		mu.Lock()
		if !loggedIPs[ip] {
			loggedIPs[ip] = true
			if err := sendDiscordEmbed("Forwarding Error", fmt.Sprintf("Failed to forward %d bytes (%s)", bytesCopied, direction), 0xFF0000); err != nil {
				log.Printf("Failed to send Discord embed: %v", err)
			}
		}
		mu.Unlock()
		return
	}
	mu.Lock()
	forwardCounts[ip]++
	if forwardCounts[ip] <= 2 {
		if !loggedForwarding[ip] {
			loggedForwarding[ip] = true
			log.Printf("forwarded %d bytes (%s)\n", bytesCopied, direction)
			if err := sendDiscordEmbed("Forwarding Success", fmt.Sprintf("Forwarded %d bytes (%s)", bytesCopied, direction), 0x008000); err != nil {
				log.Printf("Failed to send Discord embed: %v", err)
			}
		}
	}
	mu.Unlock()
	src.Close()
	dest.Close()
}

func handleClient(client net.Conn, targetAddr string) {
	defer client.Close()
	clientIP := client.RemoteAddr().String()
	ip := clientIP[:strings.Index(clientIP, ":")]
	mu.Lock()
	if !loggedIPs[ip] {
		loggedIPs[ip] = true
		log.Printf("client connected from %s\n", clientIP)
		if err := sendDiscordEmbed("Client Connected", fmt.Sprintf("New client connected from %s", clientIP), 0x008000); err != nil {
			log.Printf("Failed to send Discord embed: %v", err)
		}
	}
	mu.Unlock()
	target, err := net.Dial("tcp", targetAddr)
	if err != nil {
		log.Printf("failed to connect to backend server at %s: %v\n", targetAddr, err)
		if err := sendDiscordEmbed("Backend Connection Error", fmt.Sprintf("Failed to connect to backend server at %s: %v", targetAddr, err), 0xFF0000); err != nil {
			log.Printf("Failed to send Discord embed: %v", err)
		}
		return
	}
	defer target.Close()
	mu.Lock()
	if !loggedIPs[targetAddr] {
		loggedIPs[targetAddr] = true
		log.Printf("connected to backend server at %s\n", targetAddr)
		if err := sendDiscordEmbed("Backend Connected", fmt.Sprintf("Connected to backend server at %s", targetAddr), 0x008000); err != nil {
			log.Printf("Failed to send Discord embed: %v", err)
		}
	}
	mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(2)
	go forward(client, target, "client->backend", &wg)
	go forward(target, client, "backend->client", &wg)
	wg.Wait()
}

func startProxy(listenAddr, targetAddr string) {
	mu.Lock()
	if !loggedIPs[listenAddr] {
		loggedIPs[listenAddr] = true
		log.Printf("Attempting to starting tcp proxy on %s and forwarding to %s\n", listenAddr, targetAddr)
		if err := sendDiscordEmbed("Proxy Starting", fmt.Sprintf("Attempting to start TCP proxy on %s and forwarding to %s", listenAddr, targetAddr), 0x008000); err != nil {
			log.Printf("Failed to send Discord embed: %v", err)
		}
	}
	mu.Unlock()
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Printf("failed to start tcp proxy on %s: %v\n", listenAddr, err)
		if err := sendDiscordEmbed("Proxy Error", fmt.Sprintf("Failed to start TCP proxy on %s: %v", listenAddr, err), 0xFF0000); err != nil {
			log.Printf("Failed to send Discord embed: %v", err)
		}
		return
	}
	defer listener.Close()
	mu.Lock()
	if !loggedIPs[listenAddr] {
		loggedIPs[listenAddr] = true
		log.Printf("proxy successfully listening on %s, forwarding to %s\n", listenAddr, targetAddr)
		if err := sendDiscordEmbed("Proxy Online", fmt.Sprintf("Proxy successfully listening on %s, forwarding to %s", listenAddr, targetAddr), 0x008000); err != nil {
			log.Printf("Failed to send Discord embed: %v", err)
		}
	}
	mu.Unlock()
	for {
		client, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleClient(client, targetAddr)
	}
}

func main() {
	if len(os.Args) != 4 {
		fmt.Println("Developed by: ----------> tcp | https://t.me/bulletservices/")
		fmt.Println("usage: ./connectproxy <cncserverip> <cncscreenport> <proxyport>")
		fmt.Println("example: ./connectproxy 127.0.0.1 1111 1738") 
		return
	}
	serverIP := os.Args[1]
	backendPort := os.Args[2]
	forwardPort := os.Args[3]
	listenAddr := fmt.Sprintf("0.0.0.0:%s", forwardPort)
	targetAddr := fmt.Sprintf("%s:%s", serverIP, backendPort)
	fmt.Printf("starting tcp proxy from %s to %s\n", listenAddr, targetAddr)
	startProxy(listenAddr, targetAddr)
}
