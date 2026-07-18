# GoWork
ai agent cli tool for work

![project logo](logo/logo.svg)

## scope

this ai agent tool will focus on editing text files like coding agents\
also it will edit excell and other common files mostly code  and be able to write docs for reports or read mes 

but also

it will support skills and will allow the user to load skills  (dont know if he will be able to create his own the scope is a little big already ).

basic functionality will include context manipulation (user can also do it by hand they will be able to see witch files are in the context)

it will support multiple ai providers via api keys but the focus will be supporting local ai for free on your pc (you will have a list with the local models to select whatever you want)

and it will be able to run commands for you (you will also be able to edit them) but only if you let it do so (accept and reject states for the tui)

features will include:

change history (like gits only here you will be able to go back a change even if you didnt commit the changes)\
robust tui focused on being functional and minimal (bubble tea)\
generating deleting files and helping with git commits\
easter eggs will be included  do (just some fun things)\
some basic commands like /help , /model  they will start with /\
also a way to see token consumption (maybe a token managment tab with stats)

392 by 655 is the minimum size (the goal is for it to fit in another ides sub terminal so that we dont also make a mini ide too)

## Depedencies 
ripgrep\
packages : bubbletea , go-gitignore , godotenv

## Supported ai
the providers we support local or paid 

### Paid with api key
gemini\
groq (in the future)\
chat gpt (in the future)\
anthropic (in the future)

### local no api key needed
lmStudio (in the future)\
ollama\
llama.cpp

## FOLLOWING COMMITS FOCUS
- make the logo appear when there are 0 history messages
- add project root plus footers or styled boxes for context percent , api key , select model (they will be empty for now)
- make the chat area render markdown with the charm lib
- add a < elents which will be used in the future to hold the changes list in general it will handle all write kind tools(can be opened with cntrl e and cntr a accepts a change cntr r rejects one (and they can accept all or reject all from a file with the same binds))
- make preparations for updating the write tools with a changeState list
- update write and edit tools and their tests to acommodate for the new changeState
- implement the functionality of the change_list tab
- fix tabs ____ getting cut off
- add system prompt with the tools
- wire and init the tools in main and make a prompt with tool calls
- improve the provider interface to make it robust
- add functionality to the buttons we added long before
- extend the prompt areas functionality with features like cntr a selects everything written you can copy and paste things like that 

## TODO
- list the tuis features
- extend providers to include more (local models plus open ai gemini etch.)
- context handling
- designing skills
- actually test tool calls with the llm
- interupt handling and time excited error handling in tool calls (so that when the user wants to interupt a tool call they can or if a tool call is taking too long)
- make a tokeniser to start preparing for the stats tab
- extend the edit and write tools maybe to allow for keeping temp changes to a file
- if we do these edits to them then the tests should also change for them
- add tools to allow for web searching and scraping
- sql db for storing stats and messages and session data etch
- add slash commands 
- implement skills area
- implement token usage area stats
- tools for generating skills and handling them
- implement skill managment area 
- Important remember limit the tokens of generate so we dont burn everything in non local models

## common errors to check for api providers
invalid request\
Auth err\
Not found\
request too large (maybe)\
Api error\
Out of tokens\
rate limit\
Overload

## basic idea of the steps for coding agent
- user writes prompt
- agent starts thinking
- agent makes changes on file(s)
- user can see the files with the changes (like a list)
- they can use their ide to see the changes or they can use the tools nav
- each file selected expands and shows contents allong side a before and after the change
- the user selects accept or reject (there is also an accept and reject all button outside of the files)
- reject does nothing and accept finalises the changes

things to consider : 
maybe there should be a way for the user to prompt the ai to correct things 
while the changes havent been finalised (will inspect and consider if this is in this projects scope)

## Harness

These are the tools the agent has access to when making tool calls

### File system
read_file(path, start_line?, end_line?) - reads a file, optional line range for large files so you don't blow the context window reading a 2000 line Typst doc.\
write_file(path, old_str, new_str) - targeted find and replace, errors if old_str matches zero or more than one place.\
edit_file(path , newContent , linenums) - replace text with percision\
grep_files(path, pattern) - grep across files, returns file path and line number for each match.
create_file(path, content?) - creates a new file, errors if it already exists.\
move_file(srcpath , dstpath) - moves a file from one dir to another (it can also rename it)\
delete_file(path) - deletes a file.\
list_directory(path, recursive?) - lists files and folders, recursive flag for full project tree.\
get_file_info(path , subpath) - get usefull info about the file 

### Excel
read_excel(path, sheet?) - reads a sheet and returns it as a grid the agent can reason about. Defaults to first sheet if none specified.\
read_excel_range(path, sheet, start_row, start_col, end_row, end_col) - reads a specific range instead of the whole sheet. Critical for large spreadsheets where reading everything wastes context.\
edit_excel() - edit the contents of an excel sheet

### Skills
list_skills() - returns all available skill files so the agent can pick which ones are relevant for the current task.\
create_skil() - create and save a new skill for the llm to use
edit_skil() - edit an existing skill
delete_skil() - delete an existing skill

### WebSearch
SearchWebUrl(url) - used to search the url for docs etch or things not in training data

### Pdf
ReadPdf() - used to parse a pdf for an assigment 

## Instalation

COMING SOON

## Running GoWork

ALSO COMMING SOON

## run test

use this to run all the tests for every tool
```
make test 
```
## Keybinds

COMING SOON MAN A LOT OF THINGS ARE STILL NOT DONE LOL

## Slash commands
/help \
/stats \
/skills \
/save \
/coffee \
/new \
/save \
/key \
/context filename 

## commands to open it
gowork <project path>
