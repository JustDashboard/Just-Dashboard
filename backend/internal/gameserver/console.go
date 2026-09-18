package gameserver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ConsoleBackend is Docker's exec. The console reaches the game through the
// container's own RCON client; it never opens a shell and never runs a string
// through one.
type ConsoleBackend interface {
	ExecCheck(context.Context, string, []string, time.Duration) (int, []byte, error)
}

var (
	ErrConsoleUnavailable = errors.New("game console is unavailable")
	ErrCommandRefused     = errors.New("console command refused")
)

// commandRE is deliberately narrow: a Minecraft command is a word, then plain
// arguments. Anything carrying a shell metacharacter, a newline or a NUL is
// refused before it reaches exec, so there is no path from this field to a host
// shell even if the container's rcon client were replaced.
var commandRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,31}( [^\x00-\x1f;&|$` + "`" + `<>\\]{0,200})*$`)

// Console runs one command against a game server and returns what the game
// said. Sessions are per-command by design: a persistent shell is exactly the
// escape this surface must not have.
type Console struct {
	backend ConsoleBackend
	client  []string
}

// NewConsole builds a console for a container. The client argv is the
// blueprint's own console client — for the Minecraft images, rcon-cli.
func NewConsole(backend ConsoleBackend, client []string) *Console {
	if len(client) == 0 {
		client = []string{"rcon-cli"}
	}
	return &Console{backend: backend, client: append([]string(nil), client...)}
}

type ConsoleResult struct {
	Command    string    `json:"command"`
	Output     string    `json:"output"`
	ExitCode   int       `json:"exitCode"`
	ExecutedAt time.Time `json:"executedAt"`
}

// ValidateCommand is exported so preflight, schedules and the route all refuse
// the same commands. A schedule that can send what the console cannot would be
// the same hole with a delay on it.
func ValidateCommand(command string) (string, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), "/"))
	if trimmed == "" {
		return "", fmt.Errorf("%w: the command is empty", ErrCommandRefused)
	}
	if len(trimmed) > 512 {
		return "", fmt.Errorf("%w: the command is longer than 512 characters", ErrCommandRefused)
	}
	if !commandRE.MatchString(trimmed) {
		return "", fmt.Errorf("%w: only plain game commands are accepted here", ErrCommandRefused)
	}
	return trimmed, nil
}

func (c *Console) Run(ctx context.Context, containerID, command string) (*ConsoleResult, error) {
	if c == nil || c.backend == nil {
		return nil, ErrConsoleUnavailable
	}
	// The command is judged before the runtime is. A command this console does
	// not accept is refused whether or not the server happens to be running,
	// and the operator is told which of the two problems they have.
	safe, err := ValidateCommand(command)
	if err != nil {
		return nil, err
	}
	if containerID == "" {
		return nil, fmt.Errorf("%w: this deployment has no running game container", ErrConsoleUnavailable)
	}
	// The command is passed as separate argv elements, never as one string for
	// something else to split.
	argv := append(append([]string(nil), c.client...), strings.Fields(safe)...)
	code, output, err := c.backend.ExecCheck(ctx, containerID, argv, 20*time.Second)
	if err != nil {
		return nil, fmt.Errorf("%w: the console client could not be run", ErrConsoleUnavailable)
	}
	return &ConsoleResult{
		Command: safe, Output: sanitizeConsoleOutput(string(output)),
		ExitCode: code, ExecutedAt: time.Now().UTC(),
	}, nil
}

// sanitizeConsoleOutput strips the colour codes and control bytes a game emits,
// so the transcript is text rather than an escape-sequence delivery mechanism.
func sanitizeConsoleOutput(output string) string {
	// Whole escape sequences go, not just their introducer: dropping the ESC
	// and keeping "]0;title" would still let a game's output rewrite what the
	// reader sees around it.
	output = ansiRE.ReplaceAllString(output, "")
	var builder strings.Builder
	builder.Grow(len(output))
	runes := []rune(output)
	for index := 0; index < len(runes); index++ {
		current := runes[index]
		if current == '§' && index+1 < len(runes) {
			index++
			continue
		}
		if current == '\n' || current == '\t' {
			builder.WriteRune(current)
			continue
		}
		if current < 0x20 || current == 0x7f {
			continue
		}
		builder.WriteRune(current)
	}
	trimmed := strings.TrimRight(builder.String(), "\n")
	if len(trimmed) > 64<<10 {
		return trimmed[:64<<10] + "\n… output truncated at 64 KiB"
	}
	return trimmed
}

// Players is the online summary. Supported is false when no tested adapter can
// report identities for this game, and the UI must then hide player controls
// rather than show empty ones.
type Players struct {
	Supported  bool      `json:"supported"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	Online     int       `json:"online"`
	Maximum    int       `json:"maximum"`
	Names      []string  `json:"names"`
	ObservedAt time.Time `json:"observedAt"`
}

// listRE matches the vanilla `list` reply, which Paper, Purpur, Fabric, Forge
// and NeoForge all keep.
var listRE = regexp.MustCompile(`There are (\d+)(?:[^0-9]+)?of a max(?:imum)? of (\d+) players online`)

func ParsePlayerList(output string) (Players, bool) {
	result := Players{Supported: true, Status: "available", Names: []string{}, ObservedAt: time.Now().UTC()}
	match := listRE.FindStringSubmatch(output)
	if match == nil {
		return Players{}, false
	}
	result.Online, _ = strconv.Atoi(match[1])
	result.Maximum, _ = strconv.Atoi(match[2])
	_, names, found := strings.Cut(output, ":")
	if found {
		for _, name := range strings.Split(names, ",") {
			name = strings.TrimSpace(name)
			if name != "" && playerNameRE.MatchString(name) {
				result.Names = append(result.Names, name)
			}
		}
	}
	sort.Strings(result.Names)
	// The count the game reported is the count. A trimmed name list is a
	// parsing limit, not evidence that fewer people are playing.
	if len(result.Names) != result.Online {
		result.Reason = "The server reported " + strconv.Itoa(result.Online) +
			" online but named " + strconv.Itoa(len(result.Names)) + ". Only names this dashboard could read are listed."
	}
	return result, true
}

var (
	playerNameRE = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)
	ansiRE       = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?|[@-Z\\\\-_])")
)

// PlayerAction is the closed set of moderation commands. A blueprint cannot add
// to it, and the name is validated before it is interpolated.
type PlayerAction string

const (
	PlayerKick      PlayerAction = "kick"
	PlayerBan       PlayerAction = "ban"
	PlayerPardon    PlayerAction = "pardon"
	PlayerOp        PlayerAction = "op"
	PlayerDeop      PlayerAction = "deop"
	PlayerWhitelist PlayerAction = "whitelist_add"
	PlayerUnlist    PlayerAction = "whitelist_remove"
)

func PlayerCommand(action PlayerAction, name string) (string, error) {
	if !playerNameRE.MatchString(name) {
		return "", fmt.Errorf("%w: %q is not a Minecraft account name", ErrCommandRefused, name)
	}
	switch action {
	case PlayerKick:
		return "kick " + name, nil
	case PlayerBan:
		return "ban " + name, nil
	case PlayerPardon:
		return "pardon " + name, nil
	case PlayerOp:
		return "op " + name, nil
	case PlayerDeop:
		return "deop " + name, nil
	case PlayerWhitelist:
		return "whitelist add " + name, nil
	case PlayerUnlist:
		return "whitelist remove " + name, nil
	default:
		return "", fmt.Errorf("%w: %q is not a supported player action", ErrCommandRefused, action)
	}
}
