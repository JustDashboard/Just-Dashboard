# Preserve the operator's zsh setup while choosing our final interactive hook.
typeset -g __jd_start_agent="${JD_TERMINAL_START_AGENT:-}"
typeset -g __jd_start_dir="${JD_TERMINAL_START_DIR:-}"
unset JD_TERMINAL_START_AGENT JD_TERMINAL_START_DIR
typeset -g __jd_config_dir=$ZDOTDIR
typeset -g __jd_user_dir=$JD_ORIGINAL_ZDOTDIR
unset JD_ORIGINAL_ZDOTDIR
[[ -r $__jd_user_dir/.zshenv ]] && source "$__jd_user_dir/.zshenv"
# Per-account installers put commands in ~/.local/bin, and Debian's ~/.profile
# adds it for a login that reads ~/.profile. Root's does not, and a PATH line
# an installer wrote to ~/.bashrc never reaches zsh: both are added here the
# way that profile adds them, when they exist.
for __jd_dir in $HOME/bin $HOME/.local/bin; do
  [[ -d $__jd_dir && :$PATH: != *:$__jd_dir:* ]] && PATH=$__jd_dir:$PATH
done
unset __jd_dir
ZDOTDIR=$__jd_config_dir
