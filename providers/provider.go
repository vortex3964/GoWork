// DESC: this file contains the provider interface wich every
// other ai supported will inherit and also contains common functions
// every supported ai will use

//TODO:
//add api key setters getters
//think of other things to add that are needed everywhere for ai


//TODO:make context building to send for the api faster instead of having to rebuild 
//it every time for every new prompt inside the generate function witch currently is 
//only in the gemini file

//IMPORTANT: Generate should handle retries or handle high server demands etch
package providers

import (
	"fmt"
)

//used to model the messages in the context window may change in the future
//to be better suited for messages for code
type Message struct{
	Role string
	Content string
}

//NOTE: since ai is stateless and we send all the context everytime then
//just pass it by reference in the Generate function
type provider interface{
	Generate(userPrompt string , context []Message) (string , error)
}

//selects an ai provider (model) and returns it to the main loop
// curently only works for gemini
func Select_provider(model string , api_key string) (provider , error){
	if api_key == ""{
		return nil , fmt.Errorf("Empty api key")
	}

	return newGemini(model,api_key) , nil
}
