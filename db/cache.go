package db

import (
	"fmt"

	"github.com/genjidb/genji"
	"github.com/simpleiot/simpleiot/data"
)

// The following contains node with all its edges
type nodeAndEdges struct {
	node *data.Node
	up   []*data.Edge
	down []*data.Edge
}

type nodeEdgeCache struct {
	nodes        map[string]*nodeAndEdges
	edges        map[string]*data.Edge
	edgeModified map[string]bool
	tx           *genji.Tx
	db           *Db
}

func newNodeEdgeCache(db *Db, tx *genji.Tx) *nodeEdgeCache {
	return &nodeEdgeCache{
		nodes:        make(map[string]*nodeAndEdges),
		edges:        make(map[string]*data.Edge),
		edgeModified: make(map[string]bool),
		db:           db,
		tx:           tx,
	}
}

// this function builds a cache of edges and replaces
// the edge in the array with the one in the cache if present
// this ensures the edges in the cache are the same as the ones
// in the array. The edges parameter may be modified.
func (nec *nodeEdgeCache) cacheEdges(edges []*data.Edge) {
	for i, e := range edges {
		eCache, ok := nec.edges[e.ID]
		if !ok {
			nec.edges[e.ID] = e
		} else {
			edges[i] = eCache
		}
	}
}

// this function gets a node, all its edges, and caches it
func (nec *nodeEdgeCache) getNodeAndEdges(id string) (*nodeAndEdges, error) {
	ret, ok := nec.nodes[id]
	if ok {
		return ret, nil
	}

	ret = &nodeAndEdges{}

	node, err := nec.db.node(id)
	if err != nil {
		return ret, err
	}

	downEdges := nec.db.edgeDown(id)
	nec.cacheEdges(downEdges)

	upEdges := nec.db.edgeUp(id, true)
	nec.cacheEdges(upEdges)

	ret.node = node
	ret.up = upEdges
	ret.down = downEdges

	nec.nodes[id] = ret

	return ret, nil
}

// populate cache and update hashes for node and edges all the way up to root, and one level down from current node
func (nec *nodeEdgeCache) processNode(ne *nodeAndEdges, newEdge bool) error {
	// FIXME -- it is bad to be reaching back into the db to take this lock
	// very hard to reason about.
	// also, this cache update is not a transaction -- we need to abstract this cache
	// out better so we can control locking better
	nec.db.lock.Lock()
	updateHash(ne.node, ne.up, ne.down)
	nec.db.lock.Unlock()

	for _, e := range ne.up {
		nec.edgeModified[e.ID] = true
	}

	for _, upEdge := range ne.up {
		if upEdge.Up == "" || upEdge.Up == "none" {
			continue
		}

		neUp, err := nec.getNodeAndEdges(upEdge.Up)

		if err != nil {
			return fmt.Errorf("Error getting neUp: %w", err)
		}

		if newEdge {
			neUp.down = append(neUp.down, upEdge)
		}

		err = nec.processNode(neUp, false)

		if err != nil {
			return fmt.Errorf("Error processing node to update hash: %w", err)
		}
	}

	return nil
}

func (nec *nodeEdgeCache) writeEdges() error {
	for id := range nec.edgeModified {
		edge, ok := nec.edges[id]
		if !ok {
			return fmt.Errorf("Error could not find edge in cache: %v", id)
		}

		nec.db.lock.Lock()
		nec.db.edgeCache[id] = edge
		nec.db.lock.Unlock()

		err := nec.tx.Exec(`insert into edges values ? on conflict do replace`, edge)

		if err != nil {
			return fmt.Errorf("Error updating hash in edge %v: %v", id, err)
		}
	}

	return nil
}