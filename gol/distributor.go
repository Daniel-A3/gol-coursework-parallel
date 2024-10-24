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
	keyPresses <-chan rune
}

// Distributor divides the work between workers and interacts with other goroutines.
func distributor(p Params, c distributorChannels) {

	var mu sync.Mutex
	cond := sync.NewCond(&mu)

	// TODO: Create a 2D slice to store the world.
	world := createWorld(p.ImageHeight, p.ImageWidth)

	// Gets the world from input
	world = inputToWorld(p, world, c)

	// List of channels for workers with the size of the amount of threads that are going to be used
	channels := make([]chan [][]byte, p.Threads)

	quit := make(chan bool)
	pause := make(chan bool)
	resume := make(chan bool)

	turn := 0
	c.events <- StateChange{turn, Executing}
	ticker := time.NewTicker(2 * time.Second)
	tickerRunning := true
	gamePaused := false

	// Anonymous go routine for ticker control
	go func() {
		for {
			select {
			case <-ticker.C:
				if tickerRunning {
					mu.Lock()
					if turn != 0 {
						aliveCount, _ := calculateAliveCells(p, world)
						c.events <- AliveCellsCount{CompletedTurns: turn, CellsCount: aliveCount}
					}
					mu.Unlock()
				}
			case <-pause:
				tickerRunning = false
			case <-resume:
				tickerRunning = true
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()

	// TODO: Execute all turns of the Game of Life.

	// Recognising key presses
	go func() {
		for {
			select {
			case key := <-c.keyPresses:
				switch key {
				case 's':
					mu.Lock()
					// Puts current world into PGM file
					worldToOutput(p, world, c, turn)
					mu.Unlock()
				case 'p':
					if !gamePaused {
						pause <- true
						gamePaused = true
						fmt.Println("Paused")
						c.events <- StateChange{turn, Paused}
					} else {
						resume <- true
						gamePaused = false

						cond.Broadcast() // Resume all paused workers

						fmt.Println("Resumed")
						c.events <- StateChange{turn, Executing}
					}
				case 'q':

					mu.Lock()
					fmt.Println("Quitting...")
					terminate(p, world, c, turn)
					// Stops anonymous go routine from running, so it will not send on closed channel c
					ticker.Stop()
					// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
					close(c.events)
					mu.Unlock()
					quit <- true
					return
				}
			case <-quit:
				return
			}
		}
	}()

	for i := 0; i < p.Threads; i++ {
		channels[i] = make(chan [][]byte)
	}

	// Main loop that handles turns
	for i := 0; i < p.Turns; i++ {
		select {
		case <-quit:
			mu.Unlock()
			terminate(p, world, c, turn)
			return
		default:
			// Handle pause
			mu.Lock()
			for gamePaused {
				cond.Wait() // Wait until resume signal
				select {
				case <-quit:
					mu.Unlock()
					terminate(p, world, c, turn)
					return
				default:
				}
			}
			mu.Unlock()

			// Create a world for the next step
			newWorld := createWorld(p.ImageHeight, p.ImageWidth)

			// Create a worker for each thread
			rowsPerWorker := p.ImageHeight / p.Threads
			extraRows := p.ImageHeight % p.Threads
			startY := 0
			for w := 0; w < p.Threads; w++ {
				numRows := rowsPerWorker
				if extraRows > w {
					numRows++
				}
				endY := startY + numRows
				go worker(p, world, 0, p.ImageWidth, startY, endY, channels[w], c, i+1, quit)
				startY = endY
			}

			// Append each worker's result into the new world
			startY = 0
			for w := 0; w < p.Threads; w++ {
				numRows := rowsPerWorker
				if extraRows > w {
					numRows++
				}
				endY := startY + numRows
				copy(newWorld[startY:endY], <-channels[w])
				startY = endY
			}

			// Update the world and increment the turn
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

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	// Outputs the final world into PMG file
	worldToOutput(p, world, c, turn)

	// Stops anonymous go routine from running, so it will not send on closed channel c
	ticker.Stop()

	c.events <- StateChange{turn, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}

// Helper functions

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

func worker(p Params, world [][]byte, startX, endX, startY, endY int, out chan<- [][]byte, c distributorChannels, turn int, quit <-chan bool) {
	select {
	case out <- calculateNextState(p, world, startX, endX, startY, endY, c, turn):
	case <-quit:
		return
	}

}

// Function for creating world
func createWorld(height, width int) [][]byte {
	world := make([][]byte, height)
	for i := range world {
		world[i] = make([]byte, width)
	}
	return world
}

func inputToWorld(p Params, world [][]byte, c distributorChannels) [][]byte {
	// Read the file
	c.ioCommand <- ioInput
	c.ioFilename <- fmt.Sprintf("%dx%d", p.ImageWidth, p.ImageHeight)
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

func worldToOutput(p Params, world [][]byte, c distributorChannels, turn int) {
	// Outputs the final world into a pmg file

	c.ioCommand <- ioOutput
	fileName := fmt.Sprintf("%dx%dx%d", p.ImageWidth, p.ImageHeight, turn)
	c.ioFilename <- fileName
	// sends each cell into output channel
	for y := 0; y < p.ImageHeight; y++ {
		for x := 0; x < p.ImageWidth; x++ {
			c.ioOutput <- world[y][x]
		}
	}
	c.ioCommand <- ioCheckIdle

	c.events <- ImageOutputComplete{turn, fileName}
}

func terminate(p Params, world [][]byte, c distributorChannels, turn int) {
	finalAliveCells := make([]util.Cell, p.ImageWidth*p.ImageHeight)
	_, finalAliveCells = calculateAliveCells(p, world)

	finalState := FinalTurnComplete{p.Turns, finalAliveCells}
	c.events <- finalState
	// Outputs the final world into a pmg file
	worldToOutput(p, world, c, turn)

	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{turn, Quitting}
}
