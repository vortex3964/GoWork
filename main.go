package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/joho/godotenv"
)

func main()  {
	fmt.Println("hi this is a init test")
	
	// load the env file since its in the same dir
	err := godotenv.Load()
	
	if err != nil {
		fmt.Println("Something went wrong couldnt locate .env file")
		os.Exit(1)
	}

	var api_key string = os.Getenv("API_KEY")

	if api_key == ""{
		fmt.Println("api key is empty")
		os.Exit(1)
	}

	fmt.Println(api_key)

	reqBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": "hello"},
				},
			},
		},
	}

	jsonData, err := json.Marshal(reqBody)

	if err != nil {
		fmt.Println("err")
		os.Exit(1)
	}

	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.5-flash:generateContent"
	
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	
	if err != nil {
		fmt.Println("Failed to create request:", err)
		os.Exit(1)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", api_key)

	client := &http.Client{}
	
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Request failed:", err)
		os.Exit(1)
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	
	if err != nil {
		fmt.Println("Failed to read response:", err)
		os.Exit(1)
	}

	// define the shape of the response so we can parse into it
	type GeminiResponse struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	var geminiResp GeminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		fmt.Println("Failed to parse response:", err)
		os.Exit(1)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		fmt.Println("No text in response")
		os.Exit(1)
	}

	fmt.Println(geminiResp.Candidates[0].Content.Parts[0].Text)	

}
