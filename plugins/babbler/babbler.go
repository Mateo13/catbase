// © 2013 the CatBase Authors under the WTFPL. See AUTHORS for the list of authors.

package babbler

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/velour/catbase/bot"
	"github.com/velour/catbase/bot/msg"
	"github.com/velour/catbase/config"
)

var (
	NO_BABBLER   = errors.New("babbler not found")
	SAID_NOTHING = errors.New("hasn't said anything yet")
	NEVER_SAID   = errors.New("never said that")
)

type BabblerPlugin struct {
	Bot    bot.Bot
	db     *sqlx.DB
	config *config.Config
}

type Babbler struct {
	BabblerId int64  `db:"id"`
	Name      string `db:"babbler"`
}

type BabblerWord struct {
	WordId int64  `db:"id"`
	Word   string `db:"word"`
}

type BabblerNode struct {
	NodeId        int64 `db:"id"`
	BabblerId     int64 `db:"babblerId"`
	WordId        int64 `db:"wordId"`
	Root          int64 `db:"root"`
	RootFrequency int64 `db:"rootFrequency"`
}

type BabblerArc struct {
	ArcId      int64 `db:"id"`
	FromNodeId int64 `db:"fromNodeId"`
	ToNodeId   int64 `db:"toNodeId"`
	Frequency  int64 `db:"frequency"`
}

func New(bot bot.Bot) *BabblerPlugin {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if bot.DBVersion() == 1 {
		if _, err := bot.DB().Exec(`create table if not exists babblers (
			id integer primary key,
			babbler string
		);`); err != nil {
			log.Fatal(err)
		}

		if _, err := bot.DB().Exec(`create table if not exists babblerWords (
			id integer primary key,
			word string
		);`); err != nil {
			log.Fatal(err)
		}

		if _, err := bot.DB().Exec(`create table if not exists babblerNodes (
			id integer primary key,
			babblerId integer,
			wordId integer,
			root integer,
			rootFrequency integer
		);`); err != nil {
			log.Fatal(err)
		}

		if _, err := bot.DB().Exec(`create table if not exists babblerArcs (
			id integer primary key,
			fromNodeId integer,
			toNodeId interger,
			frequency integer
		);`); err != nil {
			log.Fatal(err)
		}
	}

	plugin := &BabblerPlugin{
		Bot:    bot,
		db:     bot.DB(),
		config: bot.Config(),
	}

	plugin.createNewWord("")

	return plugin
}

func (p *BabblerPlugin) Message(message msg.Message) bool {
	lowercase := strings.ToLower(message.Body)
	tokens := strings.Fields(lowercase)
	numTokens := len(tokens)

	saidSomething := false
	saidWhat := ""

	if numTokens >= 2 && tokens[1] == "says" {
		saidWhat, saidSomething = p.getBabble(tokens)
	} else if len(tokens) == 4 && strings.Index(lowercase, "initialize babbler for ") == 0 {
		saidWhat, saidSomething = p.initializeBabbler(tokens)
	} else if strings.Index(lowercase, "batch learn for ") == 0 {
		saidWhat, saidSomething = p.batchLearn(tokens)
	} else if len(tokens) == 5 && strings.Index(lowercase, "merge babbler") == 0 {
		saidWhat, saidSomething = p.merge(tokens)
	} else {
		//this should always return "", false
		saidWhat, saidSomething = p.addToBabbler(message.User.Name, lowercase)
	}

	if saidSomething {
		p.Bot.SendMessage(message.Channel, saidWhat)
	}
	return saidSomething
}

func (p *BabblerPlugin) Help(channel string, parts []string) {
	p.Bot.SendMessage(channel, "initialize babbler for seabass\n\nseabass says")
}

func (p *BabblerPlugin) Event(kind string, message msg.Message) bool {
	return false
}

func (p *BabblerPlugin) BotMessage(message msg.Message) bool {
	return false
}

func (p *BabblerPlugin) RegisterWeb() *string {
	return nil
}

func (p *BabblerPlugin) makeBabbler(name string) (*Babbler, error) {
	res, err := p.db.Exec(`insert into babblers (babbler) values (?);`, name)
	if err == nil {
		id, err := res.LastInsertId()
		if err != nil {
			log.Print(err)
			return nil, err
		}
		return &Babbler{
			BabblerId: id,
			Name:      name,
		}, nil
	}
	return nil, err
}

func (p *BabblerPlugin) getBabbler(name string) (*Babbler, error) {
	var bblr Babbler
	err := p.db.QueryRowx(`select * from babblers where babbler = ? LIMIT 1;`, name).StructScan(&bblr)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("failed to find babbler")
			return nil, NO_BABBLER
		}
		log.Printf("encountered problem in babbler lookup")
		log.Print(err)
		return nil, err
	}
	return &bblr, nil
}

func (p *BabblerPlugin) getOrCreateBabbler(name string) (*Babbler, error) {
	babbler, err := p.getBabbler(name)
	if err == NO_BABBLER {
		babbler, err = p.makeBabbler(name)
		if err != nil {
			log.Print(err)
			return nil, err
		}

		rows, err := p.db.Queryx(fmt.Sprintf("select tidbit from factoid where fact like '%s quotes';", babbler.Name))
		if err != nil {
			log.Print(err)
			return babbler, nil
		}
		defer rows.Close()

		tidbits := []string{}
		for rows.Next() {
			var tidbit string
			err := rows.Scan(&tidbit)

			log.Print(tidbit)

			if err != nil {
				log.Print(err)
				return babbler, err
			}
			tidbits = append(tidbits, tidbit)
		}

		for _, tidbit := range tidbits {
			if err = p.addToMarkovChain(babbler, tidbit); err != nil {
				log.Print(err)
			}
		}
	}
	return babbler, err
}

func (p *BabblerPlugin) getWord(word string) (*BabblerWord, error) {
	var w BabblerWord
	err := p.db.QueryRowx(`select * from babblerWords where word = ? LIMIT 1;`, word).StructScan(&w)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NEVER_SAID
		}
		return nil, err
	}
	return &w, nil
}

func (p *BabblerPlugin) createNewWord(word string) (*BabblerWord, error) {
	res, err := p.db.Exec(`insert into babblerWords (word) values (?);`, word)
	if err != nil {
		log.Print(err)
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		log.Print(err)
		return nil, err
	}
	return &BabblerWord{
		WordId: id,
		Word:   word,
	}, nil
}

func (p *BabblerPlugin) getOrCreateWord(word string) (*BabblerWord, error) {
	if w, err := p.getWord(word); err == NEVER_SAID {
		return p.createNewWord(word)
	} else {
		if err != nil {
			log.Print(err)
		}
		return w, err
	}
}

func (p *BabblerPlugin) getBabblerNode(babbler *Babbler, word string) (*BabblerNode, error) {
	w, err := p.getWord(word)
	if err != nil {
		return nil, err
	}

	var node BabblerNode
	err = p.db.QueryRowx(`select * from babblerNodes where babblerId = ? and wordId = ? LIMIT 1;`, babbler.BabblerId, w.WordId).StructScan(&node)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NEVER_SAID
		}
		return nil, err
	}
	return &node, nil
}

func (p *BabblerPlugin) createBabblerNode(babbler *Babbler, word string) (*BabblerNode, error) {
	w, err := p.getOrCreateWord(word)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	res, err := p.db.Exec(`insert into babblerNodes (babblerId, wordId, root, rootFrequency) values (?, ?, 0, 0)`, babbler.BabblerId, w.WordId)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		log.Print(err)
		return nil, err
	}

	return &BabblerNode{
		NodeId:        id,
		WordId:        w.WordId,
		Root:          0,
		RootFrequency: 0,
	}, nil
}

func (p *BabblerPlugin) getOrCreateBabblerNode(babbler *Babbler, word string) (*BabblerNode, error) {
	node, err := p.getBabblerNode(babbler, word)
	if err != nil {
		return p.createBabblerNode(babbler, word)
	}
	return node, nil
}

func (p *BabblerPlugin) incrementRootWordFrequency(babbler *Babbler, word string) (*BabblerNode, error) {
	node, err := p.getOrCreateBabblerNode(babbler, word)
	if err != nil {
		log.Print(err)
		return nil, err
	}
	_, err = p.db.Exec(`update babblerNodes set rootFrequency = rootFrequency + 1, root = 1 where id = ?;`, node.NodeId)
	if err != nil {
		log.Print(err)
		return nil, err
	}
	node.RootFrequency += 1
	return node, nil
}

func (p *BabblerPlugin) getBabblerArc(fromNode, toNode *BabblerNode) (*BabblerArc, error) {
	var arc BabblerArc
	err := p.db.QueryRowx(`select * from babblerArcs where fromNodeId = ? and toNodeId = ?;`, fromNode.NodeId, toNode.NodeId).StructScan(&arc)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NEVER_SAID
		}
		return nil, err
	}
	return &arc, nil
}

func (p *BabblerPlugin) incrementWordArc(fromNode, toNode *BabblerNode) (*BabblerArc, error) {
	res, err := p.db.Exec(`update babblerArcs set frequency = frequency + 1 where fromNodeId = ? and toNodeId = ?;`, fromNode.NodeId, toNode.NodeId)
	if err != nil {
		log.Print(err)
		return nil, err
	}

	affectedRows := int64(0)
	if err == nil {
		affectedRows, _ = res.RowsAffected()
	}

	if affectedRows == 0 {
		res, err = p.db.Exec(`insert into babblerArcs (fromNodeId, toNodeId, frequency) values (?, ?, 1);`, fromNode.NodeId, toNode.NodeId)
		if err != nil {
			log.Print(err)
			return nil, err
		}
	}
	return p.getBabblerArc(fromNode, toNode)
}

func (p *BabblerPlugin) incrementFinalWordArcHelper(babbler *Babbler, node *BabblerNode) (*BabblerArc, error) {
	nextNode, err := p.getOrCreateBabblerNode(babbler, " ")
	if err != nil {
		return nil, err
	}
	return p.incrementWordArc(node, nextNode)
}

func (p *BabblerPlugin) addToMarkovChain(babbler *Babbler, phrase string) error {
	words := strings.Fields(strings.ToLower(phrase))

	if len(words) <= 0 {
		return nil
	}

	curNode, err := p.incrementRootWordFrequency(babbler, words[0])
	if err != nil {
		log.Print(err)
		return err
	}

	for i := 1; i < len(words); i++ {
		nextNode, err := p.getOrCreateBabblerNode(babbler, words[i])
		if err != nil {
			log.Print(err)
			return err
		}
		_, err = p.incrementWordArc(curNode, nextNode)
		if err != nil {
			log.Print(err)
			return err
		}
		curNode = nextNode
	}

	_, err = p.incrementFinalWordArcHelper(babbler, curNode)
	return err
}

func (p *BabblerPlugin) getWeightedRootNode(babbler *Babbler) (*BabblerNode, *BabblerWord, error) {
	rows, err := p.db.Queryx(`select * from babblerNodes where babblerId = ? and root = 1;`, babbler.BabblerId)
	if err != nil {
		log.Print(err)
		return nil, nil, err
	}
	defer rows.Close()

	rootNodes := []*BabblerNode{}
	total := int64(0)

	for rows.Next() {
		var node BabblerNode
		err = rows.StructScan(&node)
		if err != nil {
			log.Print(err)
			return nil, nil, err
		}
		rootNodes = append(rootNodes, &node)
		total += node.RootFrequency
	}

	if len(rootNodes) == 0 {
		return nil, nil, SAID_NOTHING
	}

	which := rand.Int63n(total)
	total = 0
	for _, node := range rootNodes {
		total += node.RootFrequency
		if total >= which {
			var w BabblerWord
			err := p.db.QueryRowx(`select * from babblerWords where id = ? LIMIT 1;`, node.WordId).StructScan(&w)
			if err != nil {
				log.Print(err)
				return nil, nil, err
			}
			return node, &w, nil
		}

	}
	log.Fatalf("shouldn't happen")
	return nil, nil, errors.New("failed to find weighted root word")
}

func (p *BabblerPlugin) getWeightedNextWord(fromNode *BabblerNode) (*BabblerNode, *BabblerWord, error) {
	rows, err := p.db.Queryx(`select * from babblerArcs where fromNodeId = ?;`, fromNode.NodeId)
	if err != nil {
		log.Print(err)
		return nil, nil, err
	}
	defer rows.Close()

	arcs := []*BabblerArc{}
	total := int64(0)
	for rows.Next() {
		var arc BabblerArc
		err = rows.StructScan(&arc)
		if err != nil {
			log.Print(err)
			return nil, nil, err
		}
		arcs = append(arcs, &arc)
		total += arc.Frequency
	}

	if len(arcs) == 0 {
		return nil, nil, errors.New("missing arcs")
	}

	which := rand.Int63n(total)
	total = 0
	for _, arc := range arcs {

		total += arc.Frequency

		if total >= which {
			var node BabblerNode
			err := p.db.QueryRowx(`select * from babblerNodes where id = ? LIMIT 1;`, arc.ToNodeId).StructScan(&node)
			if err != nil {
				log.Print(err)
				return nil, nil, err
			}

			var w BabblerWord
			err = p.db.QueryRowx(`select * from babblerWords where id = ? LIMIT 1;`, node.WordId).StructScan(&w)
			if err != nil {
				log.Print(err)
				return nil, nil, err
			}
			return &node, &w, nil
		}

	}
	log.Fatalf("shouldn't happen")
	return nil, nil, errors.New("failed to find weighted next word")
}

func (p *BabblerPlugin) babble(who string) (string, error) {
	return p.babbleSeed(who, []string{})
}

func (p *BabblerPlugin) babbleSeed(babblerName string, seed []string) (string, error) {
	babbler, err := p.getBabbler(babblerName)
	if err != nil {
		log.Print(err)
		return "", nil
	}

	words := seed

	var curNode *BabblerNode
	var curWord *BabblerWord
	if len(seed) == 0 {
		curNode, curWord, err = p.getWeightedRootNode(babbler)
		if err != nil {
			log.Print(err)
			return "", err
		}
		words = append(words, curWord.Word)
	} else {
		curNode, err = p.getBabblerNode(babbler, seed[0])
		if err != nil {
			log.Print(err)
			return "", err
		}
		for i := 1; i < len(seed); i++ {
			nextNode, err := p.getBabblerNode(babbler, seed[i])
			if err != nil {
				log.Print(err)
				return "", err
			}
			_, err = p.getBabblerArc(curNode, nextNode)
			if err != nil {
				log.Print(err)
				return "", err
			}
			curNode = nextNode
		}
	}

	for {
		curNode, curWord, err = p.getWeightedNextWord(curNode)
		if err != nil {
			log.Print(err)
			return "", err
		}
		if curWord.Word == " " {
			break
		}
		words = append(words, curWord.Word)
	}

	return strings.TrimSpace(strings.Join(words, " ")), nil
}

func (p *BabblerPlugin) mergeBabblers(intoBabbler, otherBabbler *Babbler, intoName, otherName string) error {
	intoNode, err := p.getOrCreateBabblerNode(intoBabbler, "<"+intoName+">")
	if err != nil {
		log.Print(err)
		return err
	}
	otherNode, err := p.getOrCreateBabblerNode(otherBabbler, "<"+otherName+">")
	if err != nil {
		log.Print(err)
		return err
	}

	mapping := map[int64]*BabblerNode{}

	rows, err := p.db.Queryx("select * from babblerNodes where babblerId = ?;", otherBabbler.BabblerId)
	if err != nil {
		log.Print(err)
		return err
	}
	defer rows.Close()

	nodes := []*BabblerNode{}

	for rows.Next() {
		var node BabblerNode
		err = rows.StructScan(&node)
		if err != nil {
			log.Print(err)
			return err
		}
		nodes = append(nodes, &node)
	}

	for _, node := range nodes {
		var res sql.Result

		if node.NodeId == otherNode.NodeId {
			node.WordId = intoNode.WordId
		}

		if node.Root > 0 {
			res, err = p.db.Exec(`update babblerNodes set rootFrequency = rootFrequency + ?, root = 1 where babblerId = ? and wordId = ?;`, node.RootFrequency, intoBabbler.BabblerId, node.WordId)
			if err != nil {
				log.Print(err)
			}
		} else {
			res, err = p.db.Exec(`update babblerNodes set rootFrequency = rootFrequency + ? where babblerId = ? and wordId = ?;`, node.RootFrequency, intoBabbler.BabblerId, node.WordId)
			if err != nil {
				log.Print(err)
			}
		}

		rowsAffected := int64(-1)
		if err == nil {
			rowsAffected, _ = res.RowsAffected()
		}

		if err != nil || rowsAffected == 0 {
			res, err = p.db.Exec(`insert into babblerNodes (babblerId, wordId, root, rootFrequency) values (?,?,?,?) ;`, intoBabbler.BabblerId, node.WordId, node.Root, node.RootFrequency)
			if err != nil {
				log.Print(err)
				return err
			}
		}

		var updatedNode BabblerNode
		err = p.db.QueryRowx(`select * from babblerNodes where babblerId = ? and wordId = ? LIMIT 1;`, intoBabbler.BabblerId, node.WordId).StructScan(&updatedNode)
		if err != nil {
			log.Print(err)
			return err
		}

		mapping[node.NodeId] = &updatedNode
	}

	for oldNodeId, newNode := range mapping {
		rows, err := p.db.Queryx("select * from babblerArcs where fromNodeId = ?;", oldNodeId)
		if err != nil {
			return err
		}
		defer rows.Close()

		arcs := []*BabblerArc{}

		for rows.Next() {
			var arc BabblerArc
			err = rows.StructScan(&arc)
			if err != nil {
				log.Print(err)
				return err
			}
			arcs = append(arcs, &arc)
		}

		for _, arc := range arcs {
			_, err := p.incrementWordArc(newNode, mapping[arc.ToNodeId])
			if err != nil {
				return err
			}
		}
	}

	return err
}
