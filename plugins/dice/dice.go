// © 2013 the CatBase Authors under the WTFPL. See AUTHORS for the list of authors.

package dice

import (
	"time"

	"github.com/velour/catbase/bot"
	"github.com/velour/catbase/bot/msg"
)

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

// This is a dice plugin to serve as an example and quick copy/paste for new plugins.

type DicePlugin struct {
	Bot bot.Bot
}

// NewDicePlugin creates a new DicePlugin with the Plugin interface
func New(bot bot.Bot) *DicePlugin {
	rand.Seed(time.Now().Unix())

	return &DicePlugin{
		Bot: bot,
	}
}

func rollDie(sides int) int {
	return rand.Intn(sides) + 1
}

// Message responds to the bot hook on recieving messages.
// This function returns true if the plugin responds in a meaningful way to the users message.
// Otherwise, the function returns false and the bot continues execution of other plugins.
func (p *DicePlugin) Message(message msg.Message) bool {
	channel := message.Channel
	parts := strings.Fields(message.Body)

	if len(parts) == 1 && message.Command {
		var dice []string
		dice = strings.Split(parts[0], "d")

		if len(dice) == 2 {
			// We actually have a die roll.
			nDice, err := strconv.Atoi(dice[0])
			if err != nil {
				return false
			}

			sides, err := strconv.Atoi(dice[1])
			if err != nil {
				return false
			}

			if sides < 2 || nDice < 1 || nDice > 20 {
				p.Bot.SendMessage(channel, "You're a dick.")
				return true
			}

			rolls := fmt.Sprintf("%s, you rolled: ", message.User.Name)

			for i := 0; i < nDice; i++ {
				rolls = fmt.Sprintf("%s %d", rolls, rollDie(sides))
				if i != nDice-1 {
					rolls = fmt.Sprintf("%s,", rolls)
				} else {
					rolls = fmt.Sprintf("%s.", rolls)
				}
			}

			p.Bot.SendMessage(channel, rolls)
			return true
		}
	}
	return false
}

// Help responds to help requests. Every plugin must implement a help function.
func (p *DicePlugin) Help(channel string, parts []string) {
	p.Bot.SendMessage(channel, "Roll dice using notation XdY. Try \"3d20\".")
}

// Empty event handler because this plugin does not do anything on event recv
func (p *DicePlugin) Event(kind string, message msg.Message) bool {
	return false
}

// Handler for bot's own messages
func (p *DicePlugin) BotMessage(message msg.Message) bool {
	return false
}

// Register any web URLs desired
func (p *DicePlugin) RegisterWeb() *string {
	return nil
}

func (p *DicePlugin) ReplyMessage(message msg.Message, identifier string) bool { return false }
