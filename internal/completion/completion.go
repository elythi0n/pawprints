// Package completion holds the shell completion scripts.
package completion

// Commands lists every subcommand, for completion.
const Commands = "log show diff cat sessions turns pins stats watch restore undo pin unpin export clear prune regroup install uninstall setup doctor info theme completion agent-guide version help"

// Bash is the bash completion script.
var Bash = `# bash completion for pawprints. Source from ~/.bashrc:
#   eval "$(pawprints completion bash)"
_pawprints() {
    local cur prev words cword
    _init_completion 2>/dev/null || { cur=${COMP_WORDS[COMP_CWORD]}; prev=${COMP_WORDS[COMP_CWORD-1]}; }
    local cmds="` + Commands + `"
    case "$prev" in
        pawprints) COMPREPLY=($(compgen -W "$cmds" -- "$cur")); return ;;
        show|diff|cat|restore|pin|unpin)
            COMPREPLY=($(compgen -W "$(pawprints log --json --limit 40 2>/dev/null | sed -n 's/.*"id":"\([A-Z0-9]*\)".*/\1/p')" -- "$cur")); return ;;
        --session) COMPREPLY=($(compgen -W "current $(pawprints sessions --json 2>/dev/null | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')" -- "$cur")); return ;;
        --against) COMPREPLY=($(compgen -W "previous current" -- "$cur")); return ;;
        completion) COMPREPLY=($(compgen -W "bash zsh fish" -- "$cur")); return ;;
    esac
    case "$cur" in
        -*) COMPREPLY=($(compgen -W "--json --project --no-color --session --turn --path --since --limit --live --all --force --three-way --dry-run --yes --older-than --daily --top --before --against" -- "$cur")); return ;;
    esac
    COMPREPLY=($(compgen -W "$cmds" -- "$cur"))
}
complete -F _pawprints pawprints
`

// Zsh is the zsh completion script.
var Zsh = `#compdef pawprints
# zsh completion for pawprints. Add to ~/.zshrc:
#   eval "$(pawprints completion zsh)"
_pawprints() {
    local -a cmds
    cmds=(` + Commands + `)
    local prev=${words[CURRENT-1]}
    case "$prev" in
        show|diff|cat|restore|pin|unpin)
            local -a ids
            ids=(${(f)"$(pawprints log --json --limit 40 2>/dev/null | sed -n 's/.*"id":"\([A-Z0-9]*\)".*/\1/p')"})
            _describe 'event' ids; return ;;
        --session)
            local -a sess
            sess=(current ${(f)"$(pawprints sessions --json 2>/dev/null | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"})
            _describe 'session' sess; return ;;
        --against) _values 'against' previous current; return ;;
        completion) _values 'shell' bash zsh fish; return ;;
    esac
    if [[ $CURRENT -eq 2 ]]; then
        _describe 'command' cmds; return
    fi
    _arguments '--json' '--project:dir:_files -/' '--no-color' '--session:session' '--turn:turn' '--path:path' '--since:span' '--limit:n' '--live' '--all' '--force' '--three-way' '--dry-run' '--yes' '--older-than:span' '--daily' '--top:n' '--before' '--against:against'
}
compdef _pawprints pawprints
`

// Fish is the fish completion script.
var Fish = `# fish completion for pawprints. Save as ~/.config/fish/completions/pawprints.fish:
#   pawprints completion fish > ~/.config/fish/completions/pawprints.fish
complete -c pawprints -f
for cmd in ` + Commands + `
    complete -c pawprints -n '__fish_use_subcommand' -a $cmd
end
complete -c pawprints -n '__fish_seen_subcommand_from show diff cat restore pin unpin' -a '(pawprints log --json --limit 40 2>/dev/null | string match -r \'"id":"([A-Z0-9]+)"\' -g)'
complete -c pawprints -l session -r -a 'current (pawprints sessions --json 2>/dev/null | string match -r \'"id":"([^"]+)"\' -g)'
complete -c pawprints -l against -r -a 'previous current'
complete -c pawprints -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
for flag in json no-color live all force three-way dry-run yes daily before
    complete -c pawprints -l $flag
end
for flag in project turn path since limit older-than top
    complete -c pawprints -l $flag -r
end
`
