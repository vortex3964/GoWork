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


packages : bubbletea

supported ai : gemini for now (and will stay like that untill were ready to tackle supporting multiple ai providers)

## TODO
- tui
- read file
- get file
- edit file
- prompt
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
