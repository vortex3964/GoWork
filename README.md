# GoWork
ai agent cli tool for work 

## scope

this ai agent tool will focus on editing text files like coding agents
also it will edit excell and other common files mostly code but not only that

it will support skills and will allow the user to load skills create his own etch.

basic functionality will include context manipulation (user can also do it by hand they will be able to see witch files are in the context)

it will support multiple ai providers via api keys but the focus will be supporting local ai for free on your pc (you will have a list with the local models to select whatever you want)

and it will be able to run commands for you (you will also be able to edit them) but only if you let it do so

features will include:

change history (like gits only here you will be able to go back a change even if you didnt commit the changes)\
robust tui focused on being functional and minimal (bubble tea)\
generating deleting files and helping with git commits\
easter eggs will be included  do (just some fun things)\
some basic commands like /help , /model  they will start with /\
also a way to see token consumption

## files that i want to support for sure with skills
excel sheets\
typst\
python for writting docs\
python code structure (maybe)


packages : bubbletea , go-gitignore , godotenv

supported ai : gemini for now (and will stay like that untill were ready to tackle supporting multiple ai providers)

## TODO
- tui
- read file
- get file
- edit file
- prompt
- add a mechanism so that context persists between sessions
- cleanup the readme
- etch

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

## probably usefull function tools ill need

probably should also write descriptions so that i dont forget what they do exactly when its time to write the system prompt\
@ is used to mark that a tool has been made.

### File system
read_file(path, start_line?, end_line?) - reads a file, optional line range for large files so you don't blow the context window reading a 2000 line Typst doc.\
write_file(path, content, overwrite?) - writes a full file, errors if it already exists unless overwrite is explicitly true.\
edit_file(path, old_str, new_str) - targeted find and replace, errors if old_str matches zero or more than one place.\
create_file(path, content?) - creates a new file, errors if it already exists.\
delete_file(path) - deletes a file.\
move_file(src, dst) - moves or renames a file.\
@ list_directory(path, recursive?) - lists files and folders, recursive flag for full project tree.\
search_files(path, pattern) - grep across files, returns file path and line number for each match.
get_file_info(path , subpath) - get usefull info about the file 

### Execution — coding (probably)
run_command(command, working_dir?, timeout?) - runs any shell command, returns stdout, stderr, and exit code.\
get_diagnostics(path) - returns compiler or linter errors for a file without running the program. For Go this wraps go vet.\
compile_typst(path) - wraps typst compile but returns errors in a clean structured way.

### for excel
read_excel(path, sheet?) - reads a sheet and returns it as a grid the agent can reason about. Defaults to first sheet if none specified.\
read_excel_range(path, sheet, start_row, start_col, end_row, end_col) - reads a specific range instead of the whole sheet. Critical for large spreadsheets where reading everything wastes context.\
write_excel_cell(path, sheet, row, col, value) - writes a single cell. This is the atomic operation the agent uses after deciding what to fill in.\
list_excel_sheets(path) - returns all sheet names in a workbook so the agent knows what it's working with.\
create_excel_sheet(path, sheet_name) - adds a new sheet to an existing workbook.

### Memory and skills (some of them maybe shouldnt be used by the ai)
read_memory(key) - reads a value from the memory store by key. The agent calls this at the start of a session to reload what it knows about the project.\
write_memory(key, value) - writes a value to the memory store. The agent calls this at the end of a session to save things worth remembering.\
list_skills() - returns all available skill files so the agent can pick which ones are relevant for the current task.\
create_skill(name, content) - lets the agent write a new skill when it discovers a pattern worth noting (this may not be included)

21 more tools remain
