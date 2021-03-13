package babel

import (
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"sync"

	"github.com/dop251/goja"
)

const DefaultPoolSize = 1

type babelTransformer struct {
	Runtime   *goja.Runtime
	Transform func(string, map[string]interface{}) (goja.Value, error)
}

type Pool struct {
	once *sync.Once
	ch chan *babelTransformer
	babelProg *goja.Program
}

func Init(poolSize int) (p *Pool, err error) {
	p = &Pool{
		once: &sync.Once{},
	}
	if poolSize == 0 {
		poolSize = DefaultPoolSize
	}
	p.once.Do(func() {
		if e := p.compileBabel(); e != nil {
			err = e
			return
		}
		p.ch = make(chan *babelTransformer, poolSize)
		for i := 0; i < poolSize; i++ {
			vm := goja.New()
			transformFn, e := p.loadBabel(vm)
			if e != nil {
				err = e
				return
			}
			p.ch <- &babelTransformer{Runtime: vm, Transform: transformFn}
		}
	})

	return
}

func (p *Pool) Transform(src io.Reader, opts map[string]interface{}) (io.Reader, error) {
	data, err := ioutil.ReadAll(src)
	if err != nil {
		return nil, err
	}
	res, err := p.TransformString(string(data), opts)
	if err != nil {
		return nil, err
	}
	return strings.NewReader(res), nil
}

func (p *Pool) TransformString(src string, opts map[string]interface{}) (string, error) {
	if opts == nil {
		opts = map[string]interface{}{}
	}
	t, err := p.getTransformer()
	if err != nil {
		return "", err
	}
	defer func() {
		// Make transformer available again when we're done
		p.ch <- t
	}()
	v, err := t.Transform(src, opts)
	if err != nil {
		return "", err
	}
	vm := t.Runtime
	return v.ToObject(vm).Get("code").String(), nil
}

func (p *Pool) getTransformer() (*babelTransformer, error) {
	// Make sure we have a pool created
	if cap(p.ch) == 0 {
		return nil, fmt.Errorf("pool not initialized")
	}
	for {
		t := <-p.ch
		return t, nil
	}
}

func (p *Pool) compileBabel() error {
	babelData, err := _Asset("babel.js")
	if err != nil {
		return err
	}

	p.babelProg, err = goja.Compile("babel.js", string(babelData), false)
	if err != nil {
		return err
	}

	return nil
}

func (p *Pool) loadBabel(vm *goja.Runtime) (func(string, map[string]interface{}) (goja.Value, error), error) {
	_, err := vm.RunProgram(p.babelProg)
	if err != nil {
		return nil, fmt.Errorf("unable to load babel.js: %s", err)
	}
	var transform goja.Callable
	babel := vm.Get("Babel")
	if err := vm.ExportTo(babel.ToObject(vm).Get("transform"), &transform); err != nil {
		return nil, fmt.Errorf("unable to export transform fn: %s", err)
	}
	return func(src string, opts map[string]interface{}) (goja.Value, error) {
		return transform(babel, vm.ToValue(src), vm.ToValue(opts))
	}, nil
}
