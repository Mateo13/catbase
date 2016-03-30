// © 2013 the CatBase Authors under the WTFPL. See AUTHORS for the list of authors.

package bot

import (
	"database/sql"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/mattn/go-sqlite3"
	"github.com/velour/catbase/config"
)

// bot type provides storage for bot-wide information, configs, and database connections
type bot struct {
	// Each plugin must be registered in our plugins handler. To come: a map so that this
	// will allow plugins to respond to specific kinds of events
	plugins        map[string]Handler
	pluginOrdering []string

	// Users holds information about all of our friends
	users []User
	// Represents the bot
	me User

	config *config.Config

	conn Connector

	// SQL DB
	// TODO: I think it'd be nice to use https://github.com/jmoiron/sqlx so that
	//       the select/update/etc statements could be simplified with struct
	//       marshalling.
	db        *sqlx.DB
	dbVersion int64

	logIn  chan Message
	logOut chan Messages

	version string

	// The entries to the bot's HTTP interface
	httpEndPoints map[string]string
}

// Log provides a slice of messages in order
type Log Messages
type Messages []Message

type Logger struct {
	in      <-chan Message
	out     chan<- Messages
	entries Messages
}

func NewLogger(in chan Message, out chan Messages) *Logger {
	return &Logger{in, out, make(Messages, 0)}
}

func RunNewLogger(in chan Message, out chan Messages) {
	logger := NewLogger(in, out)
	go logger.Run()
}

func (l *Logger) sendEntries() {
	l.out <- l.entries
}

func (l *Logger) Run() {
	var msg Message
	for {
		select {
		case msg = <-l.in:
			l.entries = append(l.entries, msg)
		case l.out <- l.entries:
			go l.sendEntries()
		}
	}
}

type Message struct {
	User          *User
	Channel, Body string
	Raw           string
	Command       bool
	Action        bool
	Time          time.Time
	Host          string
}

type Variable struct {
	Variable, Value string
}

func init() {
	regex := func(re, s string) (bool, error) {
		return regexp.MatchString(re, s)
	}
	sql.Register("sqlite3_custom",
		&sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				return conn.RegisterFunc("REGEXP", regex, true)
			},
		})
}

// Newbot creates a bot for a given connection and set of handlers.
func New(config *config.Config, connector Connector) Bot {
	sqlDB, err := sqlx.Open("sqlite3_custom", config.DB.File)
	if err != nil {
		log.Fatal(err)
	}

	logIn := make(chan Message)
	logOut := make(chan Messages)

	RunNewLogger(logIn, logOut)

	users := []User{
		User{
			Name: config.Nick,
		},
	}

	bot := &bot{
		config:         config,
		plugins:        make(map[string]Handler),
		pluginOrdering: make([]string, 0),
		conn:           connector,
		users:          users,
		me:             users[0],
		db:             sqlDB,
		logIn:          logIn,
		logOut:         logOut,
		version:        config.Version,
		httpEndPoints:  make(map[string]string),
	}

	bot.migrateDB()

	http.HandleFunc("/", bot.serveRoot)
	if config.HttpAddr == "" {
		config.HttpAddr = "127.0.0.1:1337"
	}
	go http.ListenAndServe(config.HttpAddr, nil)

	connector.RegisterMessageReceived(bot.MsgReceived)
	connector.RegisterEventReceived(bot.EventReceived)

	return bot
}

// Config gets the configuration that the bot is using
func (b *bot) Config() *config.Config {
	return b.config
}

func (b *bot) DBVersion() int64 {
	return b.dbVersion
}

func (b *bot) DB() *sqlx.DB {
	return b.db
}

// Create any tables if necessary based on version of DB
// Plugins should create their own tables, these are only for official bot stuff
// Note: This does not return an error. Database issues are all fatal at this stage.
func (b *bot) migrateDB() {
	_, err := b.db.Exec(`create table if not exists version (version integer);`)
	if err != nil {
		log.Fatal("Initial DB migration create version table: ", err)
	}
	var version sql.NullInt64
	err = b.db.QueryRow("select max(version) from version").Scan(&version)
	if err != nil {
		log.Fatal("Initial DB migration get version: ", err)
	}
	if version.Valid {
		b.dbVersion = version.Int64
		log.Printf("Database version: %v\n", b.dbVersion)
	} else {
		log.Printf("No versions, we're the first!.")
		_, err := b.db.Exec(`insert into version (version) values (1)`)
		if err != nil {
			log.Fatal("Initial DB migration insert: ", err)
		}
	}

	if b.dbVersion == 1 {
		if _, err := b.db.Exec(`create table if not exists variables (
			id integer primary key,
			name string,
			perms string,
			type string
		);`); err != nil {
			log.Fatal("Initial DB migration create variables table: ", err)
		}
		if _, err := b.db.Exec(`create table if not exists 'values' (
			id integer primary key,
			varId integer,
			value string
		);`); err != nil {
			log.Fatal("Initial DB migration create values table: ", err)
		}
	}
}

// Adds a constructed handler to the bots handlers list
func (b *bot) AddHandler(name string, h Handler) {
	b.plugins[strings.ToLower(name)] = h
	b.pluginOrdering = append(b.pluginOrdering, name)
	if entry := h.RegisterWeb(); entry != nil {
		b.httpEndPoints[name] = *entry
	}
}

func (b *bot) Who(channel string) []User {
	out := []User{}
	for _, u := range b.users {
		if u.Name != b.Config().Nick {
			out = append(out, u)
		}
	}
	return out
}

var rootIndex string = `
<!DOCTYPE html>
<html>
	<head>
		<title>Factoids</title>
		<link rel="stylesheet" href="http://yui.yahooapis.com/pure/0.1.0/pure-min.css">
                <meta name="viewport" content="width=device-width, initial-scale=1">
	</head>
	{{if .EndPoints}}
	<div style="padding-top: 1em;">
		<table class="pure-table">
			<thead>
				<tr>
					<th>Plugin</th>
				</tr>
			</thead>

			<tbody>
				{{range $key, $value := .EndPoints}}
				<tr>
					<td><a href="{{$value}}">{{$key}}</a></td>
				</tr>
				{{end}}
			</tbody>
		</table>
	</div>
	{{end}}
</html>
`

func (b *bot) serveRoot(w http.ResponseWriter, r *http.Request) {
	context := make(map[string]interface{})
	context["EndPoints"] = b.httpEndPoints
	t, err := template.New("rootIndex").Parse(rootIndex)
	if err != nil {
		log.Println(err)
	}
	t.Execute(w, context)
}

// Checks if message is a command and returns its curtailed version
func IsCmd(c *config.Config, message string) (bool, string) {
	cmdc := c.CommandChar
	botnick := strings.ToLower(c.Nick)
	iscmd := false
	lowerMessage := strings.ToLower(message)

	if strings.HasPrefix(lowerMessage, cmdc) && len(cmdc) > 0 {
		iscmd = true
		message = message[len(cmdc):]
		// } else if match, _ := regexp.MatchString(rex, lowerMessage); match {
	} else if strings.HasPrefix(lowerMessage, botnick) &&
		len(lowerMessage) > len(botnick) &&
		(lowerMessage[len(botnick)] == ',' || lowerMessage[len(botnick)] == ':') {

		iscmd = true
		message = message[len(botnick):]

		// trim off the customary addressing punctuation
		if message[0] == ':' || message[0] == ',' {
			message = message[1:]
		}
	}

	// trim off any whitespace left on the message
	message = strings.TrimSpace(message)

	return iscmd, message
}
