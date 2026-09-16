ZDOTDIR=$__jd_user_dir
[[ -r $ZDOTDIR/.zshrc ]] && source "$ZDOTDIR/.zshrc"
if (( ! $+functions[compdef] )); then
  autoload -Uz compinit
  compinit
fi
zmodload zsh/complist
zstyle ':completion:*' menu select
bindkey '^I' complete-word
# zsh keeps no history file unless told to. An account whose login shell is
# bash has no .zshrc to say so, and suggestions drawn from history would then
# forget everything the moment the window closed.
[[ -n $HISTFILE ]] || HISTFILE=$__jd_user_dir/.zsh_history
(( HISTSIZE > 1000 )) || HISTSIZE=10000
(( SAVEHIST > 0 )) || SAVEHIST=10000
# Command colouring, then inline suggestions from history: each wraps the line
# editor, and the plugins' own instruction is to load in this order, after
# compinit. Debian and Fedora put them under /usr/share/<plugin>, Arch and
# Alpine under /usr/share/zsh/plugins. An account rc that already loaded one
# is left as it is rather than wrapped a second time.
__jd_plugin() {
  (( $+functions[$2] )) && return
  local file
  for file in /usr/share/$1/$1.zsh /usr/share/zsh/plugins/$1/$1.zsh; do
    [[ -r $file ]] && { source "$file"; return }
  done
}
__jd_plugin zsh-syntax-highlighting _zsh_highlight
__jd_plugin zsh-autosuggestions _zsh_autosuggest_start
unfunction __jd_plugin
__jd_prompt() { PROMPT=$'%F{cyan}%~%f\n%F{cyan}>%f '; RPROMPT=''; }
precmd_functions+=(__jd_prompt)
__jd_prompt
unset __jd_config_dir __jd_user_dir
