// Copyright 2013 The Go Circuit Project
// Use of this source code is governed by the license for
// The Go Circuit Project, found in the LICENSE file.
//
// Authors:
//   2013 Petar Maymounkov <p@gocircuit.org>

package tissue

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	//"runtime/debug"

	"github.com/gocircuit/circuit/kit/lang"
	"github.com/gocircuit/circuit/use/circuit"
	"github.com/gocircuit/circuit/use/n"
)

// Kin is the tissue system server.
type Kin struct {
	kinav KinAvatar // Permanent circuit-wide unique ID for this kin
	neighborhood *Neighborhood
	rip chan KinAvatar // denouncements of newly discovered deceased kins
	sync.Mutex
	topic map[string]FolkAvatar // topic -> yfolk
	folk []*Folk
}

const ServiceName = "kin"

func NewKin() (k *Kin, xkin XKin, rip <-chan KinAvatar) {
	k = &Kin{
		neighborhood:   NewNeighborhood(),
		rip:   make(chan KinAvatar, ExpansionHigh),
		topic: make(map[string]FolkAvatar),
	}
	// Create a KinAvatar for this system.
	k.kinav = KinAvatar(Avatar{
		X:  circuit.PermRef(XKin{k}),
		ID: lang.ComputeReceiverID(k),
	})
	return k, XKin{k}, k.rip
}

// ReJoin contacts the peering kin service join and joins its circuit network.
func (k *Kin) ReJoin(join n.Addr) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic joining: %v", r)
		}
	}()
	ykin := YKin{
		KinAvatar{
			X: circuit.Dial(join, ServiceName),
		},
	}
	var w bytes.Buffer
	for _, peer := range ykin.Join(k.chooseBoundary(Spread), Spread) {
		peer = k.remember(peer)
		w.WriteString(peer.X.Addr().String())
		w.WriteByte(' ')
	}
	// if w.Len() > 0 {
	// 	log.Println("Remembering offered server(s):", w.String())
	// }
	return nil
}

func (k *Kin) chooseBoundary(spread int) []KinAvatar {
	defer func() {
		recover()
	}()
	m := make(map[lang.ReceiverID]KinAvatar)
	m[k.kinav.ID] = k.kinav // add self in boundary offering
	for i := 0; i+1 < spread; i++ {
		peerAvatar := XKin{k}.Walk(Depth)
		if Avatar(peerAvatar).IsNil() {
			continue
		}
		if _, ok := m[peerAvatar.ID]; ok {
			// Duplicate
			continue
		}
		m[peerAvatar.ID] = peerAvatar
	}
	r := make([]KinAvatar, 0, len(m))
	for _, peerAvatar := range m {
		r = append(r, peerAvatar)
	}
	return r
}

func (k *Kin) remember(peer KinAvatar) KinAvatar {
	peer.X = ForwardPanic(
		peer.X,
		func (interface{}) {
			k.forget(peer.ID)
		},
	)
	k.neighborhood.Add(Avatar(peer))
	for _, folk := range k.users() {
		p := YKin{peer}.Attach(folk.topic)
		p.X = ForwardPanic(
			p.X,
			func (interface{}) {
				k.forget(p.ID)
			},
		)
		p.ID = peer.ID // use the kin ID
		folk.addPeer(p)
	}
	k.shrink()
	return peer
}

// If the neighborhood is too big, shrink shrinks it to size ExpansionHigh.
func (k *Kin) shrink() {
	for i := 0; i < k.neighborhood.Len() - ExpansionHigh; i++ {
		av, ok := k.neighborhood.ScrubRandom()
		if !ok {
			return
		}
		log.Printf("Evicting kin %s", av.ID.String())
	}
}

// forget removes peer from its neighborhood and announces the newly discovered death of peer to the user.
func (k *Kin) forget(key lang.ReceiverID) {
	for _, folk := range k.users() { // Remove peer from all users
		folk.removePeer(key)
	}
	peer, ok := k.neighborhood.Scrub(key) 
	if !ok {
		return
	}
	log.Printf("Forgetting kin %s", key.String())
	k.rip <- KinAvatar(peer)
	k.expand()
}

func (k *Kin) Avatar() KinAvatar {
	return k.kinav
}

// If the neighborhood is too small, expand chooses random peers to refill it.
func (k *Kin) expand() {
	if k.neighborhood.Len() < ExpansionLow {
		return
	}
	for i := 0; i < ExpansionHigh - k.neighborhood.Len(); i++ {
		w := XKin{k}.Walk(Depth) // Choose a random peer, using a random walk
		if Avatar(w).IsNil() || w.ID == k.Avatar().ID { // Compare just IDs, in case we got pointers to ourselves from elsewhere
			continue // If peer is nil or self, ignore it
		}
		w = k.remember(w)
		log.Printf("expanding tissue system with a random kin %s", w.ID.String())
	}
}

func (k *Kin) users() []*Folk {
	k.Lock()
	defer k.Unlock()
	folk := make([]*Folk, len(k.folk))
	copy(folk, k.folk)
	return folk
}

func (k *Kin) Attach(topic string, folkAvatar FolkAvatar) *Folk {
	var neighbors = k.neighborhood.View()
	peers := make([]FolkAvatar, 0, len(neighbors))
	for _, av := range neighbors {
		kinAvatar := KinAvatar(av)
		if folkAvatar := (YKin{kinAvatar}).Attach(topic); folkAvatar.X != nil {
			peers = append(peers, folkAvatar)
		}
	}
	k.Lock()
	defer k.Unlock()
	if _, present := k.topic[topic]; present {
		panic("dup attach")
	}
	folk := &Folk{
		topic: topic,
		neighborhood: NewNeighborhood(),
		kin: k,
		ch: make(chan FolkAvatar, len(peers)), // make sure initial set can be sent unblocked
	}
	for i, peer := range peers {
		// Rig the peers to be removed from the folk when their method calls cause panic
		key := neighbors[i].ID
		peer.ID = key
		peer.X = ForwardPanic(
			peer.X,
			func (interface{}) {
				k.forget(key)
			},
		)
		folk.addPeer(peer)
	}
	k.folk = append(k.folk, folk)
	k.topic[topic] = folkAvatar
	return folk
}
