package gol

import (
	"fmt"
	"uk.ac.bris.cs/gameoflife/util"
)

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
}

// distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {

	// TODO: Create a 2D slice to store the world.
	world := make([][]byte, p.ImageHeight)
	for i := range world {
		world[i] = make([]byte, p.ImageWidth)
	}

	c.ioCommand <- ioInput
	c.ioFilename <- fmt.Sprintf("%dx%d", p.ImageWidth, p.ImageHeight)
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			world[y][x] = <-c.ioInput
		}
	}

	// List of channels for workers
	channels := make([]chan [][]byte, p.Threads)

	turn := 0
	c.events <- StateChange{turn, Executing}

	// TODO: Execute all turns of the Game of Life.
	//for turn := 0; turn < p.Turns; turn++ {
	//	world = calculateNextState(p, world, 0, p.ImageWidth, 0, p.ImageHeight)
	//}
	if p.Threads == 1 {
		for i := 0; i < p.Turns; i++ {
			world = calculateNextState(p, world, 0, p.ImageWidth, 0, p.ImageHeight)
			turn++
		}
	} else {
		for i := 0; i < p.Threads; i++ {
			channels[i] = make(chan [][]byte)
		}
		for i := 0; i < p.Turns; i++ {
			// create a world for the next step
			newWorld := make([][]byte, p.ImageHeight)
			for i := range newWorld {
				newWorld[i] = make([]byte, p.ImageWidth)
			}

			// create a worker for each thread
			rowsPerWorker := p.ImageHeight / p.Threads
			extraRows := p.ImageHeight % p.Threads
			for w := 0; w < p.Threads; w++ {
				startY := w * rowsPerWorker
				endY := (w + 1) * rowsPerWorker
				if w+1 == p.Threads { // Last worker might get extra rows
					endY += extraRows
				}
				go worker(p, world, 0, p.ImageWidth, startY, endY, channels[w])
			}
			// append each workers result into a new world
			for c := 0; c < p.Threads; c++ {
				startY := c * rowsPerWorker
				endY := startY + rowsPerWorker
				if c == p.Threads-1 { // Last worker handles extra rows
					endY += p.ImageHeight % p.Threads
				}
				copy(newWorld[startY:endY], <-channels[c])
			}
			world = newWorld
			turn++
		}
	}
	// TODO: Report the final state using FinalTurnCompleteEvent.

	finalAliveCells := make([]util.Cell, p.ImageWidth*p.ImageHeight)
	_, finalAliveCells = calculateAliveCells(p, world)

	finalState := FinalTurnComplete{p.Turns, finalAliveCells}

	c.events <- finalState

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}

func calculateNextState(p Params, world [][]byte, startX, endX, startY, endY int) [][]byte {
	height := endY - startY
	width := endX - startX
	nextWorld := make([][]byte, height)

	for i := range nextWorld {
		nextWorld[i] = make([]byte, width)
	}

	countAlive := func(y, x int) int {
		alive := 0
		for i := -1; i < 2; i++ {
			for j := -1; j < 2; j++ {
				neighbourY := (y + i + p.ImageHeight) % p.ImageHeight
				neighbourX := (x + j + p.ImageWidth) % p.ImageWidth
				if !(i == 0 && j == 0) && (world[neighbourY][neighbourX] == 255) {
					alive++
				}
			}
		}
		return alive
	}

	for y := startY; y < endY; y++ {
		for x := startX; x < endX; x++ {
			aliveNeighbour := countAlive(y, x)

			if world[y][x] == 255 {
				if aliveNeighbour < 2 || aliveNeighbour > 3 {
					nextWorld[y-startY][x] = 0
				} else {
					nextWorld[y-startY][x] = 255
				}
			} else {
				if aliveNeighbour == 3 {
					nextWorld[y-startY][x] = 255
				} else {
					nextWorld[y-startY][x] = 0
				}
			}
		}
	}

	return nextWorld
}

func calculateAliveCells(p Params, world [][]byte) (int, []util.Cell) {
	var alive []util.Cell
	count := 0
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			if world[y][x] == 255 {
				count++
				alive = append(alive, util.Cell{X: x, Y: y})
			}
		}
	}
	return count, alive
}

func worker(p Params, world [][]byte, startX, endX, startY, endY int, out chan<- [][]byte) {
	out <- calculateNextState(p, world, startX, endX, startY, endY)
}
