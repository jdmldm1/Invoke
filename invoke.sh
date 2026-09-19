#!/usr/bin/env bash

if [ -n "$INVOKE_EXE" ] && [ -x "$INVOKE_EXE" ]; then
    _ptBin="$INVOKE_EXE"
else
    _ptBin="invoke"
fi
_ptMarks="$HOME/.invoke_marks.json"
_ptZFile="$HOME/.invoke_z.json"

function Open-PtView() {
    local kind=$1
    local file=$2
    local abs=$(readlink -f "$file" 2>/dev/null || echo "$PWD/$file")

    if [ -n "$INVOKE_HOST" ]; then
        curl -s -X POST "$INVOKE_HOST/open" \
            -H "Content-Type: application/json" \
            -d "{\"type\":\"$kind\",\"file\":\"$abs\"}" > /dev/null
        if [ $? -eq 0 ]; then
            echo -e "\e[90mOpened $kind for $(basename "$abs") in a new tab.\e[0m"
            return
        else
            echo -e "\e[33mCould not reach the Invoke window; opening standalone.\e[0m"
        fi
    fi
    "$_ptBin" "$kind" "$file"
}

function pt() {
    local sub=$1
    shift
    
    case "$sub" in
        help|term|explain|ask|commit|review|do|git|ports|env)
            "$_ptBin" "$sub" "$@"
            ;;
        edit|diff)
            Open-PtView "$sub" "$1"
            ;;
        *)
            "$_ptBin" help
            ;;
    esac
}

alias iv=pt
alias invoke=pt

alias g='git'
alias gs='git status -sb'
alias ga='git add'
alias gaa='git add -A'
alias gco='git checkout'
alias gcb='git checkout -b'
alias gd='git diff'
alias gds='git diff --staged'
alias gst='git stash'
alias gsp='git stash pop'
alias gcommit='git commit -m'
alias gamend='git commit --amend'
alias gpush='git push'
alias gpull='git pull'
alias gfetch='git fetch --all --prune'
alias gbranch='git branch'
alias glog='git log --oneline --graph --decorate -20'
alias glg="git log --graph --pretty=format:'%C(yellow)%h%Creset %C(cyan)%an%Creset %s %C(green)(%cr)%Creset' -25"

alias ..='cd ..'
alias ...='cd ../..'
alias ....='cd ../../..'
alias ll='ls -alF'
alias la='ls -A'
function mkcd() { mkdir -p "$1" && cd "$1"; }

function killport() {
    local port=$1
    if [ -z "$port" ]; then return 1; fi
    fuser -k "${port}/tcp"
}

function serve() {
    local port=${1:-8000}
    if command -v python3 &>/dev/null; then
        echo -e "\e[36mServing $PWD at http://localhost:$port (Ctrl+C to stop)\e[0m"
        python3 -m http.server $port
    elif command -v npx &>/dev/null; then
        echo -e "\e[36mServing via npx serve at port $port (Ctrl+C to stop)\e[0m"
        npx --yes serve -l $port
    fi
}

function json() {
    if command -v jq &>/dev/null; then
        jq .
    else
        python3 -m json.tool
    fi
}

function mark() {
    local name=${1:-$(basename "$PWD")}
    local tmp=$(mktemp)
    if [ -f "$_ptMarks" ]; then
        cat "$_ptMarks" > "$tmp"
    else
        echo "{}" > "$tmp"
    fi
    jq --arg k "$name" --arg v "$PWD" '.[$k] = $v' "$tmp" > "$_ptMarks"
    rm -f "$tmp"
    echo -e "\e[32mBookmarked '$name' -> $PWD\e[0m"
}
function j() {
    local name=$1
    if [ ! -f "$_ptMarks" ]; then return 1; fi
    local target=$(jq -r ".\"$name\" // empty" "$_ptMarks")
    if [ -n "$target" ] && [ -d "$target" ]; then
        cd "$target"
    fi
}
function marks() {
    if [ -f "$_ptMarks" ]; then
        jq -r 'to_entries | .[] | "  \u001b[36m\(.key | (.[0:16] + "                ")[0:16])\u001b[0m \u001b[90m\(.value)\u001b[0m"' "$_ptMarks"
    fi
}

function z() {
    if [ -z "$1" ]; then return; fi
    if [ -f "$_ptMarks" ]; then
        local target=$(jq -r --arg q "$1" 'to_entries | map(select(.key | contains($q))) | .[0].value // empty' "$_ptMarks")
        if [ -n "$target" ] && [ -d "$target" ]; then
            cd "$target"
            return
        fi
    fi
}

_ptTrackDir() {
    :
}

_invoke_prompt() {
    local last_exit=$?
    
    local r="\[\e[0m\]"
    local dim="\[\e[38;2;97;110;136m\]"
    local blue="\[\e[38;2;129;161;193m\]"
    local cyan="\[\e[38;2;216;222;233m\]"
    local green="\[\e[38;2;163;190;140m\]"
    local red="\[\e[38;2;191;97;106m\]"
    local yellow="\[\e[38;2;235;203;139m\]"
    local mag="\[\e[38;2;180;142;173m\]"
    local sky="\[\e[38;2;129;161;193m\]"

    local bar=$'\u258c'
    local sep=$'\u2502'
    local arr=$'\u276f'
    local xmark=$'\u2718'
    local dot=$'\u00b7'

    local p="${PWD/#$HOME/\~}"
    local line="${sky}${bar}${r} ${cyan}${p}${r}"

    local branchName=""
    local dirtyFlag="0"
    local staged="0"
    local unstaged="0"
    local untracked="0"

    if command -v git >/dev/null 2>&1; then
        branchName=$(git branch --show-current 2>/dev/null)
        if [ -n "$branchName" ]; then
            local status=$(git status --porcelain 2>/dev/null)
            if [ -n "$status" ]; then
                dirtyFlag="1"
                untracked=$(echo "$status" | grep -c "^??")
                staged=$(echo "$status" | grep -c "^[AMRCD]")
                unstaged=$(echo "$status" | grep -c "^.[AMRCD]")
                line+="  ${dim}${sep}${r} ${yellow}${branchName}*${r}"
            else
                line+="  ${dim}${sep}${r} ${green}${branchName}${r}"
            fi
        fi
    fi

    local segs=""
    if [ -n "$VIRTUAL_ENV" ]; then
        segs+="${mag}($(basename "$VIRTUAL_ENV"))${r}  "
    fi
    segs+="${blue}$(date +%H:%M:%S)${r}"
    if [ $last_exit -ne 0 ]; then
        segs+="  ${red}${xmark} ${last_exit}${r}"
    fi

    line+="  ${dim}${dot}${r}  ${segs}"

    local symColor=$green
    if [ $last_exit -ne 0 ]; then symColor=$red; fi

    local osc="\[\e]7000;${PWD}|${branchName}|${dirtyFlag}|${staged}|${unstaged}|${untracked}|${last_exit}\a\]"
    PS1="${osc}\n${line}\n${symColor}${arr}${r} "
}

if [ -n "$BASH_VERSION" ]; then
    PROMPT_COMMAND="_invoke_prompt"
elif [ -n "$ZSH_VERSION" ]; then
    precmd() { _invoke_prompt }
fi
