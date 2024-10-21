package gol

import (
	"fmt"
	"sync"
	"time"
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

	var mu sync.Mutex

	// TODO: Create a 2D slice to store the world.
	world := createWorld(p.ImageHeight, p.ImageWidth)

	// Read the file
	c.ioCommand <- ioInput
	c.ioFilename <- fmt.Sprintf("%dx%d", p.ImageWidth, p.ImageHeight)
	world = puttingInputIntoWorld(p, world, c)

	// List of channels for workers with the size of the amount of threads that are going to be used
	channels := make([]chan [][]byte, p.Threads)

	turn := 0
	c.events <- StateChange{turn, Executing}
	// TODO: Execute all turns of the Game of Life.
	ticker := time.NewTicker(2 * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				if turn != 0 {
					aliveCount, _ := calculateAliveCells(p, world)
					c.events <- AliveCellsCount{CompletedTurns: turn, CellsCount: aliveCount}
				}
				mu.Unlock()
			}
		}
	}()
	if p.Threads == 1 {
		for i := 0; i < p.Turns; i++ {
			mu.Lock()
			world = calculateNextState(p, world, 0, p.ImageWidth, 0, p.ImageHeight, c, i+1)
			turn++
			mu.Unlock()
			c.events <- TurnComplete{turn}
		}
	} else {
		for i := 0; i < p.Threads; i++ {
			channels[i] = make(chan [][]byte)
		}
		for i := 0; i < p.Turns; i++ {
			// create a world for the next step
			newWorld := createWorld(p.ImageHeight, p.ImageWidth)
			// create a worker for each thread
			//
			// !!! COME BACK AND MAYBE TRY TO OPTIMISE
			// Last worker takes all remainders in some cases
			// !!!
			rowsPerWorker := p.ImageHeight / p.Threads
			extraRows := p.ImageHeight % p.Threads
			startY := 0
			for w := 0; w < p.Threads; w++ {
				// Rows for current worker
				numRows := rowsPerWorker
				// Add a row if there are extra rows for workers than need doing
				if extraRows > w {
					numRows++
				}
				endY := startY + numRows
				go worker(p, world, 0, p.ImageWidth, startY, endY, channels[w], c, i+1)
				startY = endY
			}

			// append each workers result into a new world
			startY = 0
			for c := 0; c < p.Threads; c++ {
				numRows := rowsPerWorker
				// Add a row if there are extra rows for workers than need doing
				if extraRows > c {
					numRows++
				}
				endY := startY + numRows
				copy(newWorld[startY:endY], <-channels[c])
				startY = endY
			}
			mu.Lock()
			world = newWorld
			turn++
			mu.Unlock()
			c.events <- TurnComplete{turn}

		}
	}

	// TODO: Report the final state using FinalTurnCompleteEvent.
	finalAliveCells := make([]util.Cell, p.ImageWidth*p.ImageHeight)
	_, finalAliveCells = calculateAliveCells(p, world)

	finalState := FinalTurnComplete{p.Turns, finalAliveCells}
	c.events <- finalState

	// Outputs the final world into a pmg file
	c.ioCommand <- ioOutput
	fileName := fmt.Sprintf("%dx%dx%d", p.ImageWidth, p.ImageHeight, p.Turns)
	c.ioFilename <- fileName
	c.events <- ImageOutputComplete{p.Turns, fileName}
	// sends each cell into output channel
	go puttingWorldIntoOutput(p, world, c)

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	// Stops anonymous go routine from running, so it will not send on closed channel c
	ticker.Stop()

	c.events <- StateChange{turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}

func calculateNextState(p Params, world [][]byte, startX, endX, startY, endY int, c distributorChannels, turn int) [][]byte {
	height := endY - startY
	width := endX - startX
	nextWorld := createWorld(height, width)

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
					c.events <- CellFlipped{turn, util.Cell{X: x, Y: y}}
				} else {
					nextWorld[y-startY][x] = 255
				}
			} else {
				if aliveNeighbour == 3 {
					nextWorld[y-startY][x] = 255
					c.events <- CellFlipped{turn, util.Cell{X: x, Y: y}}
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

func worker(p Params, world [][]byte, startX, endX, startY, endY int, out chan<- [][]byte, c distributorChannels, turn int) {
	out <- calculateNextState(p, world, startX, endX, startY, endY, c, turn)
}

// Function for creating world
func createWorld(height, width int) [][]byte {
	world := make([][]byte, height)
	for i := range world {
		world[i] = make([]byte, width)
	}
	return world
}

func puttingInputIntoWorld(p Params, world [][]byte, c distributorChannels) [][]byte {
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			world[y][x] = <-c.ioInput
			if world[y][x] == 255 {
				c.events <- CellFlipped{0, util.Cell{X: x, Y: y}}
			}
		}
	}
	return world
}

func puttingWorldIntoOutput(p Params, world [][]byte, c distributorChannels) {
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			c.ioOutput <- world[y][x]
		}
	}
}
