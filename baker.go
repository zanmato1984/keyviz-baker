package main

import (
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"sync"
	"time"
)

const MaxColor = 256

type xBaker struct {
	stmt *sql.Stmt
}

func makeXBaker(db *sql.DB, name string, y int) (*xBaker, error) {
	xb := xBaker{}
	var err error = nil
	xb.stmt, err = db.Prepare(fmt.Sprintf("select * from %s.t_%d where i < ?", name, y))
	return &xb, err
}

func (xb *xBaker) close() {
	_ = xb.stmt.Close()
}

func (xb *xBaker) bake(ripeness int) error {
	_, err := xb.stmt.Exec(ripeness)
	return err
}

type yBaker struct {
	ripenessFactor float64
}

func makeYBaker(ripenessFactor float64) *yBaker {
	return &yBaker{ripenessFactor: ripenessFactor}
}

func (yb *yBaker) bake(image image.Image, xBakers []*xBaker, x int) {
	ny := len(xBakers)
	var wg sync.WaitGroup
	wg.Add(ny)
	for i := range xBakers {
		y := i
		xb := xBakers[y]
		ripeness := int(yb.ripenessFactor * float64(color.GrayModel.Convert(image.At(x, ny-1-y)).(color.Gray).Y))

		go func() {
			defer wg.Done()
			err := xb.bake(ripeness)
			if err != nil {
				panic(err.Error())
			}
		}()
	}
	wg.Wait()
}

type Baker struct {
	name     string
	image    image.Image
	ripeness uint
	db       *sql.DB
}

func MakeBaker(name, imagePath string, ripeness uint, db string) (*Baker, error) {
	b := Baker{
		name:     name,
		ripeness: ripeness,
	}
	var err error = nil
	b.image, err = func() (image.Image, error) {
		f, err := os.Open(imagePath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		image, err := png.Decode(f)
		return image, err
	}()
	if err != nil {
		return &b, err
	}

	b.db, err = sql.Open("mysql", db)
	return &b, err
}

func (b *Baker) Close() {
	_ = b.db.Close()
}

func (b *Baker) Prepare() error {
	_, err := b.db.Exec(fmt.Sprintf("drop database if exists %s", b.name))
	if err != nil {
		return err
	}

	_, err = b.db.Exec(fmt.Sprintf("create database %s", b.name))
	if err != nil {
		return err
	}

	ny := b.image.Bounds().Dy()
	var wg sync.WaitGroup
	wg.Add(ny)
	for y := 0; y < ny; y++ {
		err := func() error {
			_, err := b.db.Exec(fmt.Sprintf("drop table if exists %s.t_%d", b.name, y))
			if err != nil {
				return err
			}

			_, err = b.db.Exec(fmt.Sprintf("create table %s.t_%d(i int primary key)", b.name, y))
			if err != nil {
				return err
			}

			insert := strings.Builder{}
			insert.WriteString(fmt.Sprintf("insert into %s.t_%d values", b.name, y))
			for r := uint(0); r < b.ripeness; r++ {
				if r == 0 {
					insert.WriteString(fmt.Sprintf("(%d)", r))
				} else {
					insert.WriteString(fmt.Sprintf(", (%d)", r))
				}
			}
			go func() {
				_, err = b.db.Exec(insert.String())
				if err != nil {
					panic(err.Error())
				}
				wg.Done()
			}()
			return nil
		}()
		if err != nil {
			return nil
		}
	}
	wg.Wait()
	return nil
}

func (b *Baker) AlignTime(alignSec int) {
	ticker := time.NewTicker(1 * time.Second)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				now := time.Now()
				if now.Second() == alignSec {
					close(done)
				}
			case <-done:
				return
			}
		}
	}()
}

func (b *Baker) Bake(intervalSec uint) error {
	nx := b.image.Bounds().Dx()
	ny := b.image.Bounds().Dy()
	xBakers := make([]*xBaker, ny)
	var err error = nil
	for y := range xBakers {
		xBakers[y], err = makeXBaker(b.db, b.name, y)
		if err != nil {
			return err
		}
	}
	defer func() {
		for _, xb := range xBakers {
			xb.close()
		}
	}()

	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	done := make(chan struct{})
	ripenessFactor := float64(b.ripeness) / MaxColor
	x := 0
	var wg sync.WaitGroup
	wg.Add(nx)
	go func() {
		for {
			select {
			case <-ticker.C:
				if x >= nx {
					close(done)
				}
				go func() {
					yb := makeYBaker(ripenessFactor)
					yb.bake(b.image, xBakers, x)
					wg.Done()
				}()
				x++
			case <-done:
				return
			}
		}
	}()
	wg.Wait()
	return err
}
