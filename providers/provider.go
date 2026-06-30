// DESC: this file contains the provider interface wich every
// other ai supported will inherit and also contains common functions
// every supported ai will use

//TODO:
//add api key setters getters
//think of other things to add that are needed everywhere for ai

//IMPORTANT: Generate should handle retries or handle high server demands etch
package providers

import (
	"fmt"
)

type provider interface{
	//NOTE:no context yet keeping it simple
	Generate(userPrompt string) (string , error)
}

//selects an ai provider (model) and returns it to the main loop
// curently only works for gemini
func Select_provider(model string , api_key string) (provider , error){
	if api_key == ""{
		return nil , fmt.Errorf("Empty api key")
	}

	return newGemini(model,api_key) , nil
}
