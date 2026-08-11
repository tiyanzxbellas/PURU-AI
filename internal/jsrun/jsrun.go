// Package divjs wraps a goja JavaScript runtime to do two things previously
// done with `new Function` in TypeScript:
//   - run user-supplied cheerio-style JS for the crawl tool (a cheerio-compatible
//     shim over goquery), and
//   - evaluate math expressions for calculate_math.
//
// The shim only exposes the subset of the cheerio API that the AI commonly
// uses (text, html, attr, val, length, first, last, eq, find, parent,
// children, filter, each, map, get, toArray). Unknown calls return a Go-side
// JS error which the tool surfaces.
package jsrun

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
)

const cheerioShim = `
function __stringify(v){
  if (v === undefined || v === null) return 'null';
  if (typeof v === 'string') return v;
  if (typeof v === 'number') return String(v);
  if (typeof v === 'boolean') return String(v);
  try { return JSON.stringify(v); } catch (e) { return String(v); }
}
var console = {};
var __ctrls = ['log','info','warn','error','debug','trace','dir','table'];
for (var i = 0; i < __ctrls.length; i++) {
  (function(k){
    console[k] = function(){ __capture(Array.prototype.slice.call(arguments).map(String).join(' ')); };
  })(__ctrls[i]);
}
function __S(h) {
  var o = {
    h: h,
    text: function(){ return __sText(h); },
    html: function(){ var s = __sHTML(h); return s === null ? undefined : s; },
    toHTML: function(){ var s = __sHTML(h); return s === null ? undefined : s; },
    attr: function(n){ return __sAttr(h, n); },
    val: function(){ var v = __sVal(h); return v === null ? undefined : v; },
    first: function(){ return __S(__sFirst(h)); },
    last: function(){ return __S(__sLast(h)); },
    eq: function(i){ return __S(__sEq(h, Number(i))); },
    find: function(s){ return __S(__sFind(h, String(s))); },
    parent: function(){ return __S(__sParent(h)); },
    parents: function(){ return __S(__sParents(h)); },
    children: function(){ return __S(__sChildren(h)); },
    filter: function(s){ return __S(__sFilter(h, String(s))); },
    each: function(fn){ __sEach(h, fn); return o; },
    map: function(fn){ var out = []; __sEach(h, function(i, sub){ out.push(fn(i, __S(sub))); }); return out; },
    get: function(i){ return __sGet(h, Number(i)); },
    toArray: function(){ return __sToArray(h); },
    length: 0
  };
  o.length = __sLen(h);
  return o;
}
function $(sel){ 
  if (sel && typeof sel === 'object' && sel.h && typeof sel.h === 'number') { return sel; }
  return __S(__selRoot()).find(sel);
}
`

type selectionHandle struct {
	vm   *goja.Runtime
	next int
	sel  map[int]*goquery.Selection
}

func toIntArg(v goja.Value) int {
	return int(v.ToInteger())
}

func (s *selectionHandle) add(sel any) int {
	var qs *goquery.Selection
	switch t := sel.(type) {
	case *goquery.Selection:
		qs = t
	case *goquery.Document:
		qs = t.Selection
	default:
		return 0
	}
	if s.sel == nil {
		s.sel = map[int]*goquery.Selection{}
	}
	s.next++
	s.sel[s.next] = qs
	return s.next
}

func (s *selectionHandle) get(h int) *goquery.Selection {
	if qs, ok := s.sel[h]; ok && qs != nil {
		return qs
	}
	empty, _ := goquery.NewDocumentFromReader(strings.NewReader("<html></html>"))
	return empty.Selection
}

type jsr struct {
	vm *goja.Runtime
	sh *selectionHandle
}

// RunCheerio extracts data from html using the given cheerio-style JS. It
// returns the serialized result plus captured console output.
func RunCheerio(html, code string) (result string, consoleOutput string, err error) {
	doc, derr := goquery.NewDocumentFromReader(strings.NewReader(html))
	if derr != nil {
		return "", "", derr
	}
	vm := goja.New()
	sh := &selectionHandle{vm: vm, next: 0}
	var capture []string

	vm.Set("__selRoot", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.add(doc)))
	})
	vm.Set("__sText", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(sh.get(toIntArg(call.Argument(0))).Text())
	})
	vm.Set("__sHTML", func(call goja.FunctionCall) goja.Value {
		s, err := goquery.OuterHtml(sh.get(toIntArg(call.Argument(0))))
		if err != nil {
			return goja.Null()
		}
		return vm.ToValue(s)
	})
	vm.Set("__sAttr", func(call goja.FunctionCall) goja.Value {
		v, ok := sh.get(toIntArg(call.Argument(0))).Attr(call.Argument(1).String())
		if !ok {
			return goja.Null()
		}
		return vm.ToValue(v)
	})
	vm.Set("__sVal", func(call goja.FunctionCall) goja.Value {
		s := sh.get(toIntArg(call.Argument(0)))
		if v, ok := s.Attr("value"); ok {
			return vm.ToValue(v)
		}
		return goja.Null()
	})
	vm.Set("__sLen", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.get(toIntArg(call.Argument(0))).Length()))
	})
	vm.Set("__sFirst", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.add(sh.get(toIntArg(call.Argument(0))).First())))
	})
	vm.Set("__sLast", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.add(sh.get(toIntArg(call.Argument(0))).Last())))
	})
	vm.Set("__sEq", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.add(sh.get(toIntArg(call.Argument(0))).Eq(int(call.Argument(1).ToInteger())))))
	})
	vm.Set("__sFind", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.add(sh.get(toIntArg(call.Argument(0))).Find(call.Argument(1).String()))))
	})
	vm.Set("__sParent", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.add(sh.get(toIntArg(call.Argument(0))).Parent())))
	})
	vm.Set("__sParents", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.add(sh.get(toIntArg(call.Argument(0))).Parents())))
	})
	vm.Set("__sChildren", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.add(sh.get(toIntArg(call.Argument(0))).Children())))
	})
	vm.Set("__sFilter", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(int64(sh.add(sh.get(toIntArg(call.Argument(0))).Filter(call.Argument(1).String()))))
	})
	vm.Set("__sEach", func(call goja.FunctionCall) goja.Value {
		handle := call.Argument(0).ToInteger()
		fn, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			return goja.Undefined()
		}
		s := sh.get(int(handle))
		n := s.Length()
		for i := 0; i < n; i++ {
			sub := int64(sh.add(s.Eq(i)))
			_, _ = fn(goja.Undefined(), vm.ToValue(i), vm.ToValue(sub))
		}
		return goja.Undefined()
	})
	vm.Set("__sMap", func(call goja.FunctionCall) goja.Value {
		handle := call.Argument(0).ToInteger()
		fn, ok := goja.AssertFunction(call.Argument(1))
		if !ok {
			return vm.ToValue([]any{})
		}
		s := sh.get(int(handle))
		n := s.Length()
		results := make([]any, 0, n)
		for i := 0; i < n; i++ {
			sub := sh.add(s.Eq(i))
			val, err := fn(goja.Undefined(), vm.ToValue(i), vm.ToValue(sub))
			if err != nil {
				panic(err)
			}
			results = append(results, val.Export())
		}
		return vm.ToValue(results)
	})
	vm.Set("__sGet", func(call goja.FunctionCall) goja.Value {
		s := sh.get(toIntArg(call.Argument(0)))
		i := int(call.Argument(1).ToInteger())
		if s.Length() <= i {
			return goja.Null()
		}
		return vm.ToValue(s.Eq(i).Text())
	})
	vm.Set("__sToArray", func(call goja.FunctionCall) goja.Value {
		s := sh.get(toIntArg(call.Argument(0)))
		out := make([]any, 0, s.Length())
		s.Each(func(_ int, ss *goquery.Selection) {
			out = append(out, map[string]any{"textContent": ss.Text()})
		})
		return vm.ToValue(out)
	})
	vm.Set("__capture", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) > 0 {
			capture = append(capture, call.Argument(0).String())
		}
		return goja.Undefined()
	})

	if _, err := vm.RunString(cheerioShim); err != nil {
		return "", "", errors.New("jsrun: cheerio shim failed to compile")
	}

	program := "(function(){\"use strict\"; return (" + normalizeCheerioCode(code) + ");}())"
	interrupt := time.AfterFunc(10*time.Second, func() { vm.Interrupt("cheerio script timeout") })
	defer interrupt.Stop()
	val, rerr := vm.RunString(program)
	if rerr != nil {
		var it *goja.InterruptedError
		if errors.As(rerr, &it) {
			return "", "", errors.New("script timeout")
		}
		return "", "", errors.New(rerr.Error())
	}
	result = stringifyValue(vm, val)
	consoleOutput = strings.Join(capture, "\n")
	return result, consoleOutput, nil
}

// normalizeCheerioCode makes the tool tolerant of the two ways the model
// writes snippets: a bare expression ("$(\"h1\").text()") or a statement with
// a leading return ("return {...};"), which would otherwise be a syntax error
// once wrapped in the evaluator's own return.
func normalizeCheerioCode(code string) string {
	c := strings.TrimSpace(code)
	if strings.HasPrefix(c, "return") && (len(c) == len("return") || c[len("return")] == ' ' || c[len("return")] == '\t' || c[len("return")] == '\n') {
		c = strings.TrimSpace(c[len("return"):])
	}
	c = strings.TrimSpace(c)
	c = strings.TrimSuffix(c, ";")
	return strings.TrimSpace(c)
}

func stringifyValue(vm *goja.Runtime, v goja.Value) string {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return "null"
	}
	strFn, _ := goja.AssertFunction(vm.Get("__stringify"))
	if strFn != nil {
		out, err := strFn(goja.Undefined(), v)
		if err == nil {
			return out.String()
		}
	}
	return v.String()
}

// EvalMath evaluates a JavaScript math expression. Common Math.* helpers are
// exposed as globals (sqrt, pow, ln/cmd log, abs, round, ...) for convenience.
func EvalMath(expr string) (string, error) {
	vm := goja.New()
	interrupt := time.AfterFunc(5*time.Second, func() { vm.Interrupt("math timeout") })
	defer interrupt.Stop()
	if _, err := vm.RunString(mathAliases); err != nil {
		return "", errors.New("Ekspresi matematika tidak valid")
	}
	val, err := vm.RunString("(function(){ return (" + expr + ");}())")
	if err != nil {
		return "", errors.New("Ekspresi matematika tidak valid")
	}
	// goja follows IEEE-754: expressions like 10/0 evaluate to Infinity and
	// 0/0 to NaN instead of throwing. Those are not valid math results — surface
	// them as an error rather than handing the model "Infinity".
	if f := val.ToFloat(); math.IsInf(f, 0) || math.IsNaN(f) {
		return "", errors.New("Ekspresi matematika tidak valid")
	}
	return stringifyValue(vm, val), nil
}

const mathAliases = `
var sqrt   = Math.sqrt, pow = Math.pow, log2 = Math.log2;
var log    = Math.log, log10 = Math.log10, abs = Math.abs;
var floor  = Math.floor, ceil = Math.ceil, round = Math.round;
var min = Math.min, max = Math.max, sin = Math.sin, cos = Math.cos;
var tan = Math.tan, PI = Math.PI, E = Math.E;`
