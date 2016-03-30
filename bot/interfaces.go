// © 2016 the CatBase Authors under the WTFPL license. See AUTHORS for the list of authors.

package bot

import (
	"github.com/jmoiron/sqlx"
	"github.com/velour/catbase/config"
)

type Bot interface {
	Config() *config.Config
	DBVersion() int64
	DB() *sqlx.DB
	Who(string) []User
	AddHandler(string, Handler)
	SendMessage(string, string)
	SendAction(string, string)
	MsgReceived(Message)
	EventReceived(Message)
	Filter(Message, string) string
	LastMessage(string) (Message, error)
}

type Connector interface {
	RegisterEventReceived(func(message Message))
	RegisterMessageReceived(func(message Message))

	SendMessage(channel, message string)
	SendAction(channel, message string)
	Serve()
}

// Interface used for compatibility with the Plugin interface
type Handler interface {
	Message(message Message) bool
	Event(kind string, message Message) bool
	BotMessage(message Message) bool
	Help(channel string, parts []string)
	RegisterWeb() *string
}
