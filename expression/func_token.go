//  Copyright (c) 2014 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
//  except in compliance with the License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing, software distributed under the
//  License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
//  either express or implied. See the License for the specific language governing permissions
//  and limitations under the License.

package expression

import (
	"regexp"

	"github.com/couchbase/query/value"
)

///////////////////////////////////////////////////
//
// ContainsToken
//
///////////////////////////////////////////////////

type ContainsToken struct {
	FunctionBase
}

func NewContainsToken(operands ...Expression) Function {
	rv := &ContainsToken{
		*NewFunctionBase("contains_token", operands...),
	}

	rv.expr = rv
	return rv
}

/*
Visitor pattern.
*/
func (this *ContainsToken) Accept(visitor Visitor) (interface{}, error) {
	return visitor.VisitFunction(this)
}

func (this *ContainsToken) Type() value.Type { return value.BOOLEAN }

func (this *ContainsToken) Evaluate(item value.Value, context Context) (value.Value, error) {
	return this.Eval(this, item, context)
}

/*
If this expression is in the WHERE clause of a partial index, lists
the Expressions that are implicitly covered.

For boolean functions, simply list this expression.
*/
func (this *ContainsToken) FilterCovers(covers map[string]value.Value) map[string]value.Value {
	covers[this.String()] = value.TRUE_VALUE
	return covers
}

func (this *ContainsToken) Apply(context Context, args ...value.Value) (value.Value, error) {
	source := args[0]
	token := args[1]

	if source.Type() == value.MISSING || token.Type() == value.MISSING {
		return value.MISSING_VALUE, nil
	} else if source.Type() == value.NULL || token.Type() == value.NULL {
		return value.NULL_VALUE, nil
	}

	options := _EMPTY_OPTIONS
	if len(args) >= 3 {
		switch args[2].Type() {
		case value.OBJECT:
			options = args[2]
		case value.MISSING:
			return value.MISSING_VALUE, nil
		default:
			return value.NULL_VALUE, nil
		}
	}

	contains := source.ContainsToken(token, options)
	return value.NewValue(contains), nil
}

func (this *ContainsToken) MinArgs() int { return 2 }

func (this *ContainsToken) MaxArgs() int { return 3 }

/*
Factory method pattern.
*/
func (this *ContainsToken) Constructor() FunctionConstructor {
	return NewContainsToken
}

///////////////////////////////////////////////////
//
// ContainsTokenLike
//
///////////////////////////////////////////////////

type ContainsTokenLike struct {
	FunctionBase
	re   *regexp.Regexp
	part *regexp.Regexp
}

func NewContainsTokenLike(operands ...Expression) Function {
	rv := &ContainsTokenLike{
		*NewFunctionBase("contains_token_like", operands...),
		nil,
		nil,
	}

	rv.re, rv.part, _ = precompileLike(operands[1].Value())
	rv.expr = rv
	return rv
}

/*
Visitor pattern.
*/
func (this *ContainsTokenLike) Accept(visitor Visitor) (interface{}, error) {
	return visitor.VisitFunction(this)
}

func (this *ContainsTokenLike) Type() value.Type { return value.BOOLEAN }

func (this *ContainsTokenLike) Evaluate(item value.Value, context Context) (value.Value, error) {
	return this.Eval(this, item, context)
}

/*
If this expression is in the WHERE clause of a partial index, lists
the Expressions that are implicitly covered.

For boolean functions, simply list this expression.
*/
func (this *ContainsTokenLike) FilterCovers(covers map[string]value.Value) map[string]value.Value {
	covers[this.String()] = value.TRUE_VALUE
	return covers
}

func (this *ContainsTokenLike) Apply(context Context, args ...value.Value) (value.Value, error) {
	source := args[0]
	pattern := args[1]

	if source.Type() == value.MISSING || pattern.Type() == value.MISSING {
		return value.MISSING_VALUE, nil
	} else if source.Type() == value.NULL || pattern.Type() != value.STRING {
		return value.NULL_VALUE, nil
	}

	options := _EMPTY_OPTIONS
	if len(args) >= 3 {
		switch args[2].Type() {
		case value.OBJECT:
			options = args[2]
		case value.MISSING:
			return value.MISSING_VALUE, nil
		default:
			return value.NULL_VALUE, nil
		}
	}

	re := this.re
	if re == nil {
		var err error
		re, _, err = likeCompile(pattern.Actual().(string))
		if err != nil {
			return nil, err
		}
	}

	matcher := func(token interface{}) bool {
		str, ok := token.(string)
		if !ok {
			return false
		}

		return re.MatchString(str)
	}

	contains := source.ContainsMatchingToken(matcher, options)
	return value.NewValue(contains), nil
}

func (this *ContainsTokenLike) MinArgs() int { return 2 }

func (this *ContainsTokenLike) MaxArgs() int { return 3 }

/*
Factory method pattern.
*/
func (this *ContainsTokenLike) Constructor() FunctionConstructor {
	return NewContainsTokenLike
}

///////////////////////////////////////////////////
//
// ContainsTokenRegexp
//
///////////////////////////////////////////////////

type ContainsTokenRegexp struct {
	FunctionBase
	re   *regexp.Regexp
	part *regexp.Regexp
	err  error
}

func NewContainsTokenRegexp(operands ...Expression) Function {
	rv := &ContainsTokenRegexp{
		*NewFunctionBase("contains_token_regexp", operands...),
		nil,
		nil,
		nil,
	}

	rv.re, _ = precompileRegexp(operands[1].Value(), true)
	rv.part, rv.err = precompileRegexp(operands[1].Value(), false)
	rv.expr = rv
	return rv
}

/*
Visitor pattern.
*/
func (this *ContainsTokenRegexp) Accept(visitor Visitor) (interface{}, error) {
	return visitor.VisitFunction(this)
}

func (this *ContainsTokenRegexp) Type() value.Type { return value.BOOLEAN }

func (this *ContainsTokenRegexp) Evaluate(item value.Value, context Context) (value.Value, error) {
	return this.Eval(this, item, context)
}

/*
If this expression is in the WHERE clause of a partial index, lists
the Expressions that are implicitly covered.

For boolean functions, simply list this expression.
*/
func (this *ContainsTokenRegexp) FilterCovers(covers map[string]value.Value) map[string]value.Value {
	covers[this.String()] = value.TRUE_VALUE
	return covers
}

func (this *ContainsTokenRegexp) Apply(context Context, args ...value.Value) (value.Value, error) {
	source := args[0]
	pattern := args[1]

	if source.Type() == value.MISSING || pattern.Type() == value.MISSING {
		return value.MISSING_VALUE, nil
	} else if source.Type() == value.NULL || pattern.Type() != value.STRING {
		return value.NULL_VALUE, nil
	}

	options := _EMPTY_OPTIONS
	if len(args) >= 3 {
		switch args[2].Type() {
		case value.OBJECT:
			options = args[2]
		case value.MISSING:
			return value.MISSING_VALUE, nil
		default:
			return value.NULL_VALUE, nil
		}
	}

	/* MB-20677 make sure full regexp doesn't skew RegexpLike
	   into accepting wrong partial regexps
	*/
	if this.err != nil {
		return nil, this.err
	}

	fullRe := this.re
	partRe := this.part

	if partRe == nil {
		var err error
		s := pattern.Actual().(string)

		/* MB-20677 ditto */
		partRe, err = regexp.Compile(s)
		if err != nil {
			return nil, err
		}
		fullRe, err = regexp.Compile("^" + s + "$")
		if err != nil {
			return nil, err
		}
	}

	matcher := func(token interface{}) bool {
		str, ok := token.(string)
		if !ok {
			return false
		}

		return fullRe.MatchString(str)
	}

	contains := source.ContainsMatchingToken(matcher, options)
	return value.NewValue(contains), nil
}

func (this *ContainsTokenRegexp) MinArgs() int { return 2 }

func (this *ContainsTokenRegexp) MaxArgs() int { return 3 }

/*
Factory method pattern.
*/
func (this *ContainsTokenRegexp) Constructor() FunctionConstructor {
	return NewContainsTokenRegexp
}

///////////////////////////////////////////////////
//
// Tokens
//
///////////////////////////////////////////////////

/*
MB-20850. Enumerate list of all tokens within the operand. For
strings, this is the list of discrete words within the string. For all
other atomic JSON values, it is the operand itself. For arrays, all
the individual array elements are tokenized. And for objects, the
names are included verbatim, while the values are tokenized.
*/
type Tokens struct {
	FunctionBase
}

func NewTokens(operands ...Expression) Function {
	rv := &Tokens{
		*NewFunctionBase("tokens", operands...),
	}

	rv.expr = rv
	return rv
}

/*
Visitor pattern.
*/
func (this *Tokens) Accept(visitor Visitor) (interface{}, error) {
	return visitor.VisitFunction(this)
}

func (this *Tokens) Type() value.Type { return value.ARRAY }

func (this *Tokens) Evaluate(item value.Value, context Context) (value.Value, error) {
	return this.Eval(this, item, context)
}

func (this *Tokens) Apply(context Context, args ...value.Value) (value.Value, error) {
	arg := args[0]
	if arg.Type() == value.MISSING {
		return value.MISSING_VALUE, nil
	}

	options := _EMPTY_OPTIONS
	if len(args) >= 2 {
		switch args[1].Type() {
		case value.OBJECT:
			options = args[1]
		case value.MISSING:
			return value.MISSING_VALUE, nil
		default:
			return value.NULL_VALUE, nil
		}
	}

	set := _SET_POOL.Get()
	defer _SET_POOL.Put(set)
	set = arg.Tokens(set, options)
	items := set.Items()
	return value.NewValue(items), nil
}

func (this *Tokens) MinArgs() int { return 1 }

func (this *Tokens) MaxArgs() int { return 2 }

/*
Factory method pattern.
*/
func (this *Tokens) Constructor() FunctionConstructor {
	return NewTokens
}

var _EMPTY_OPTIONS = value.NewValue(map[string]interface{}{})
