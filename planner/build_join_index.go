//  Copyright (c) 2014 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package planner

import (
	"fmt"

	"github.com/couchbase/query/algebra"
	"github.com/couchbase/query/datastore"
	"github.com/couchbase/query/errors"
	"github.com/couchbase/query/expression"
	"github.com/couchbase/query/plan"
	"github.com/couchbase/query/value"
)

func (this *builder) buildIndexJoin(keyspace datastore.Keyspace,
	node *algebra.IndexJoin) (op *plan.IndexJoin, err error) {

	index, covers, filterCovers, err := this.buildJoinScan(keyspace, node.Right(), "join")
	if err != nil {
		return nil, err
	}

	scan := plan.NewIndexJoin(keyspace, node, index, covers, filterCovers)
	if covers != nil {
		this.coveringScans = append(this.coveringScans, scan)
	}
	return scan, nil
}

func (this *builder) buildIndexNest(keyspace datastore.Keyspace,
	node *algebra.IndexNest) (op *plan.IndexNest, err error) {

	index, _, _, err := this.buildJoinScan(keyspace, node.Right(), "nest")
	if err != nil {
		return nil, err
	}

	return plan.NewIndexNest(keyspace, node, index), nil
}

func (this *builder) buildJoinScan(keyspace datastore.Keyspace, node *algebra.KeyspaceTerm, op string) (
	datastore.Index, expression.Covers, map[*expression.Cover]value.Value, error) {

	formalizer := expression.NewSelfFormalizer(node.Alias(), nil)
	allindexes := _INDEX_POOL.Get()
	defer _INDEX_POOL.Put(allindexes)
	allindexes, err := allIndexes(keyspace, nil, allindexes, this.indexApiVersion)
	if err != nil {
		return nil, nil, nil, err
	}

	onkey := node.JoinKeys()
	indexes := _INDEX_POOL.Get()
	defer _INDEX_POOL.Put(indexes)
	for _, index := range allindexes {
		keys := index.RangeKey()
		if len(keys) == 0 {
			continue
		}

		key, err := formalizer.Map(keys[0].Copy())
		if err != nil {
			return nil, nil, nil, err
		}

		dnf := NewDNF(key, true, true)
		key, err = dnf.Map(key)
		if err != nil {
			return nil, nil, nil, err
		}

		if onkey.EquivalentTo(key) && !indexHasArrayIndexKey(index) {
			indexes = append(indexes, index)
		}

	}

	if len(indexes) == 0 {
		return nil, nil, nil, errors.NewNoIndexJoinError(node.Alias(), op)
	}

	var pred expression.Expression
	pred = expression.NewIsNotNull(node.JoinKeys().Copy())
	dnf := NewDNF(pred, true, true)
	pred, err = dnf.Map(pred)
	if err != nil {
		return nil, nil, nil, err
	}

	subset := pred
	if kspace, ok := this.baseKeyspaces[node.Alias()]; ok {
		err = combineFilters(kspace, false)
		if err != nil {
			return nil, nil, nil, err
		}

		if kspace.dnfPred != nil {
			subset = expression.NewAnd(subset, kspace.dnfPred.Copy())
			dnf = NewDNF(subset, true, true)
			subset, err = dnf.Map(subset)
			if err != nil {
				return nil, nil, nil, err
			}
		}
	} else {
		return nil, nil, nil, errors.NewPlanInternalError(fmt.Sprintf("buildJoinScan: keyspace %s not found", node.Alias()))
	}

	primaryKey := expression.Expressions{
		expression.NewField(
			expression.NewMeta(expression.NewConstant(node.Alias())),
			expression.NewFieldName("id", false)),
	}

	sargables, _, _, err := this.sargableIndexes(indexes, pred, subset, primaryKey, formalizer)
	if err != nil {
		return nil, nil, nil, err
	}

	minimals := minimalIndexes(sargables, false, pred)
	if len(minimals) == 0 {
		return nil, nil, nil, errors.NewNoIndexJoinError(node.Alias(), op)
	}

	return this.buildCoveringJoinScan(minimals, node, op, pred)
}

func (this *builder) buildCoveringJoinScan(secondaries map[datastore.Index]*indexEntry,
	node *algebra.KeyspaceTerm, op string, pred expression.Expression) (
	datastore.Index, expression.Covers, map[*expression.Cover]value.Value, error) {

	if this.cover != nil && op == "join" {
		alias := node.Alias()
		id := expression.NewField(
			expression.NewMeta(expression.NewIdentifier(alias)),
			expression.NewFieldName("id", false))

		exprs := this.cover.Expressions()

	outer:
		for index, entry := range secondaries {
			keys := entry.keys
			if !index.IsPrimary() {
				keys = append(keys, id)
			}

			// Include covering expression from index WHERE clause
			coveringExprs, filterCovers, err := indexCoverExpressions(entry, keys, nil, nil)
			if err != nil {
				return nil, nil, nil, err
			}

			for _, expr := range exprs {
				if !expression.IsCovered(expr, alias, coveringExprs) {
					continue outer
				}
			}

			covers := make(expression.Covers, 0, len(keys))
			for _, key := range keys {
				covers = append(covers, expression.NewCover(key))
			}

			return index, covers, filterCovers, nil
		}
	}

	secondaries = minimalIndexes(secondaries, true, pred)

	for index, _ := range secondaries {
		return index, nil, nil, nil
	}

	return nil, nil, nil, errors.NewNoIndexJoinError(node.Alias(), op)
}
