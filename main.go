package main

import (
	"bufio"
	"fmt"
	"os"

	"github.com/joho/godotenv"

	"GoWork/providers"
)

func main()  {
	fmt.Println("hi this is a init test")
	
	// load the env file since its in the same dir
	err := godotenv.Load()
	
	if err != nil {
		fmt.Println("Something went wrong couldnt locate .env file ",err)
		os.Exit(1)
	}

	var api_key string = os.Getenv("API_KEY")

	if api_key == ""{
		fmt.Println("api key is empty")
		os.Exit(1)
	}

	fmt.Println(api_key)

	model , err := providers.Select_provider("gemini-3.5-flash",api_key)

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	
	scanner := bufio.NewScanner(os.Stdin)
	run := true

	for run {
    	fmt.Print("prompt: ")
    	scanner.Scan()
    	prompt := scanner.Text()
		
		if prompt == "end" {
			break
		}

    	response, err := model.Generate(prompt)

    	if err != nil {
        	fmt.Println("error:", err)
        	continue
    	}

    	fmt.Println(response)
	}

}
