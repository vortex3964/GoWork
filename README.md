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

ai will print in a read me style text

in providers list when were selecting pressing / starts filtering
## Depedencies 
ripgrep\
packages : bubbletea , go-gitignore , godotenv , sahilm/fuzzy

## Supported ai
the providers we support local or paid 

### Paid with api key
gemini\
groq\
chat gpt\
anthropic

### local no api key needed
lmStudio\
ollama\
llama.cpp

## TODO
- 6. update read and edit so that we handle pdfs and images we will ignore images and read pdfs with the new tool we will make in the future
- 1. update move delete tools to also track files with the changes list command and the watcer
- @ update move delete websearch webfetch to have a permision window from the user before running
- @ adding bash tool (some bash commands need permisions)
- @ add debug mode
- * fix bugs in file explorer it expands beyond it size when we run on smaller windows
- * file explorer doesnt have stylized text for code
- * stylize file explorers border with filename and % identation
- * changes list has pending issues
- * reject accept changes should be displayed in message stylized
- * changes list doesnt wrap around when we reach the final changes
- * no render logic on smaller windows its displayed poorly
- * needs to have a section for explaining the key binds
- * proper resizing for changes list
- designing skills
- sql db for storing stats and messages and session data etch
- add slash commands (with window for autocomplete)
- implement skills area
- implement token usage area stats
- tools for generating skills and handling them
- implement skill managment area \
- add skills loading guidance to the system prompt once skills are actually supported
- maybe make llama and lmstuido have an in app launch button in case the user forgot to start them
- maybe add lsp support?
- 4. add pdf support
- 5. handle images
- make an installer
- add a prompt message queue or block while the ai is generating (would prefer the first solution)
- mcp support?
- excell support
- @ handling permisions for tool calls from ai
- make docs
- ? have atatchments like images pdfs or copied and pasted text on the promptbar so that a large copied text doesnt take up the entire screen
- ? add a way to copy text from promptbar and message area
- ? update prompt bar to also grow up to 5 rows instead of being just 2 lines staticaly
- multithread (probably) what we do in main to avoid slugish start times
- 3. add tools to track todos or plan mode maybe
- 2. add the ability for the ai to create questioners for users that they answer with options

## common errors we check for 
invalid request\
Auth err\
Not found\
request too large (maybe)\
Api error\
Out of tokens\
rate limit\
Overload

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

### Skills
list_skills() - returns all available skill files so the agent can pick which ones are relevant for the current task.\
create_skil() - create and save a new skill for the llm to use
edit_skil() - edit an existing skill
delete_skil() - delete an existing skill

### WebSearch
SearchWebUrl(url) - used to search the url for docs etch or things not in training data

### Pdf
ReadPdf() - used to parse a pdf for an assigment 

will use pymupdf4llm as default since its small but if user has marker installed it will default to it\
(we dont force marker on everyone cause its too big)

The goal is to not force a big install on anyone. Instead, we will probe at runtime and pick the best available backend.\
this will be a python sub process the tui app will use 

Run() {
    if python3 -c "import marker" works → use marker 
    else if python3 -c "import pymupdf4llm" works → use pymupdf4llm 
    else if command -v pdftotext exists → use pdftotext
    else → return error with install instructions
}
The install instructions would recommend the lightweight option first and go as follows in the installel:

#### Install the recommended parser (Most lightweight no gpu needed):
pip install pymupdf4llm

#### For higher accuracy with OCR/tables/equations (Heaviest but better results)
pip install marker-pdf

## Instalation

COMING SOON

## Running GoWork

ALSO COMMING SOON

## run test

use this to run all the tests for every tool and other tests
```
make test 
```
## Keybinds

some of the basic keybinds may we will add more

cntrl i -> interupt(global)\

tab -> next tab (idle)\
shift tab -> previous tab (idle)

cntr j -> new line in prompt mode (prompt mode)

cntrl p -> select provider and model (any mode)

NOTE: accept and reject fail if the user wants to accept something as the ai is making changes

cntrl l -> enter changes mode\
cntrl a -> accept current change (if on header accepts all changes from file)\
cntrl r -> reject current change (if on header rejects all changes from file)\
cntrl f -> accept all changes from the ai\
cntrl d -> reject all changes from the ai

cntrl a -> copy prompt content to clipboard (prompt mode)\
cntrl v -> paste into promptbar (prompt mode)\
cntrl u -> clear the prompt bar (prompt mode)

## Slash commands
/help \
/stats \
/skills \
/save \
/coffee \
/new \
/save \
/key \
/save\
/fork\
/compact\
/context filename 

also an autofil window pops up that lazily matches

## commands to open it
gowork <project path>

