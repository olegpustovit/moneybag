package dashboard

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/tiendc/go-deepcopy"
	"golang.org/x/sync/errgroup"
)

type sourceMemo struct{ entries sync.Map }
type sourceResult struct {
	once sync.Once
	rows []map[string]any
	err  error
}

type queryResult struct {
	value any
	cost  uint64
	count int
}

func (x *execution) cachedSourceQuery(q Query) (any, error) {
	sources, ok := x.engine.Reader.(*Sources)
	if !ok || sources.Snapshots == nil || (q.From != "postings" && q.From != "journal" && q.From != "effective_entries" && !strings.HasPrefix(q.From, "view:")) {
		return x.query(q)
	}
	encoded, err := json.Marshal(x.vars["params"])
	var canonical any
	if err != nil || json.Unmarshal(encoded, &canonical) != nil || !reflect.DeepEqual(canonical, x.vars["params"]) {
		return x.query(q)
	}
	key := struct {
		Query  Query
		Params any
	}{q, x.vars["params"]}
	value, err := sources.Snapshots.read(x.ctx, x.p, "query", q.From, key, func() (any, error) {
		v, err := x.query(q)
		return queryResult{v, x.cost, x.count}, err
	}, func(v any) bool {
		// shortcut: query snapshots are limited to 1 MiB; use byte-aware eviction before caching larger results.
		return json.NewEncoder(&byteBudget{1024 * 1024}).Encode(v.(queryResult).value) == nil
	})
	if err != nil {
		return nil, err
	}
	result := value.(queryResult)
	x.cost, x.count = result.cost, result.count
	if result.value == nil {
		return nil, nil
	}
	var copied any
	if err := deepcopy.Copy(&copied, result.value); err != nil {
		x.cost, x.count = 0, 0
		return x.query(q)
	}
	return copied, nil
}

func (m *sourceMemo) read(key string, fn func() ([]map[string]any, error)) ([]map[string]any, error) {
	value, _ := m.entries.LoadOrStore(key, &sourceResult{})
	result := value.(*sourceResult)
	result.once.Do(func() { result.rows, result.err = fn() })
	return result.rows, result.err
}

// loadQueries evaluates statically known dependencies once in parallel levels.
// shortcut: dynamic data indexing uses the serial resolver; build runtime dependency tracing to parallelize it.
func (x *execution) loadQueries(data map[string]Query, issue func(string, error)) bool {
	deps := make(map[string]map[string]bool, len(data))
	dynamic := false
	for key, query := range data {
		deps[key] = map[string]bool{}
		if from, ok := strings.CutPrefix(query.From, "query:"); ok {
			deps[key][from] = true
		}
		walkExpressions(Spec{Data: map[string]Query{key: query}}, func(_ string, expr string) {
			refs, unknown, err := x.engine.Expr.DataReferences(expr)
			if err != nil {
				issue("/data/"+key, err)
				return
			}
			dynamic = dynamic || unknown
			for _, ref := range refs {
				deps[key][ref] = true
			}
		})
	}
	if dynamic {
		return false
	}
	pending, failed := map[string]bool{}, map[string]error{}
	for key := range data {
		pending[key] = true
	}
	for len(pending) != 0 {
		if err := x.ctx.Err(); err != nil {
			issue("/data", err)
			return true
		}
		keys := make([]string, 0, len(pending))
		for key := range pending {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ready := []string{}
		for _, key := range keys {
			blocked := false
			for dep := range deps[key] {
				if _, exists := data[dep]; !exists {
					failed[key] = fmt.Errorf("dependency %s not found", dep)
					delete(pending, key)
					blocked = true
					break
				}
				if failed[dep] != nil {
					failed[key] = fmt.Errorf("dependency %s failed", dep)
					delete(pending, key)
					blocked = true
					break
				}
				if pending[dep] {
					blocked = true
				}
			}
			if !blocked {
				ready = append(ready, key)
			}
		}
		if len(ready) == 0 {
			if len(pending) > 0 {
				// A failed dependency can become visible on the next pass.
				progress := false
				for _, key := range keys {
					if !pending[key] {
						progress = true
						break
					}
				}
				if progress {
					continue
				}
				for key := range pending {
					failed[key] = fmt.Errorf("cyclic query dependencies")
					delete(pending, key)
				}
			}
			break
		}
		type result struct {
			value any
			err   error
			cost  uint64
			count int
		}
		results := make([]result, len(ready))
		if x.memo == nil {
			x.memo = &sourceMemo{}
		}
		var group errgroup.Group
		group.SetLimit(4)
		for i, key := range ready {
			i, query := i, data[key]
			group.Go(func() error {
				child := &execution{ctx: x.ctx, engine: x.engine, p: x.p, vars: x.vars, memo: x.memo}
				if len(deps[key]) == 0 {
					results[i].value, results[i].err = child.cachedSourceQuery(query)
				} else {
					results[i].value, results[i].err = child.query(query)
				}
				results[i].cost, results[i].count = child.cost, child.count
				return nil
			})
		}
		_ = group.Wait()
		for i, key := range ready {
			delete(pending, key)
			r := results[i]
			x.cost += r.cost
			x.count += r.count
			if r.err != nil {
				failed[key] = r.err
				continue
			}
			if x.count > 5000000 || x.cost > 100000000 {
				issue("/data", fmt.Errorf("page evaluation budget exceeded"))
				return true
			}
			if x.dataBudget == nil {
				x.dataBudget = &byteBudget{8 * 1024 * 1024}
			}
			if err := x.dataBudget.spend(r.value); err != nil {
				issue("/data", err)
				return true
			}
			x.vars["data"].(map[string]any)[key] = r.value
		}
	}
	keys := make([]string, 0, len(failed))
	for key := range failed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		issue("/data/"+key, failed[key])
	}
	return true
}
