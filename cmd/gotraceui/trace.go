package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"honnef.co/go/gotraceui/trace"
)

type schedulingState uint8

const (
	stateNone schedulingState = iota

	// Goroutine states
	stateInactive
	stateActive
	stateGCIdle
	stateGCDedicated
	stateBlocked
	stateBlockedSend
	stateBlockedRecv
	stateBlockedSelect
	stateBlockedSync
	stateBlockedSyncOnce
	stateBlockedSyncTriggeringGC
	stateBlockedCond
	stateBlockedNet
	stateBlockedGC
	stateBlockedSyscall
	stateStuck
	stateReady
	stateCreated
	stateDone
	stateGCMarkAssist
	stateGCSweep

	// Processor states
	stateRunningG

	stateLast
)

var legalStateTransitions = [256][stateLast]bool{
	stateInactive: {
		stateActive:         true,
		stateReady:          true,
		stateBlockedSyscall: true,

		// Starting back into preempted mark assist
		stateGCMarkAssist: true,
	},
	stateActive: {
		// active -> ready occurs on preemption
		stateReady:                   true,
		stateInactive:                true,
		stateBlocked:                 true,
		stateBlockedSend:             true,
		stateBlockedRecv:             true,
		stateBlockedSelect:           true,
		stateBlockedSync:             true,
		stateBlockedSyncOnce:         true,
		stateBlockedSyncTriggeringGC: true,
		stateBlockedCond:             true,
		stateBlockedNet:              true,
		stateBlockedGC:               true,
		stateBlockedSyscall:          true,
		stateStuck:                   true,
		stateDone:                    true,
		stateGCMarkAssist:            true,
		stateGCSweep:                 true,
	},
	stateGCIdle: {
		// active -> ready occurs on preemption
		stateReady:       true,
		stateInactive:    true,
		stateBlockedSync: true,
	},
	stateGCDedicated: {
		// active -> ready occurs on preemption
		stateReady:       true,
		stateInactive:    true,
		stateBlockedSync: true,
	},
	stateCreated: {
		stateActive: true,

		// FIXME(dh): These three transitions are only valid for goroutines that already existed when tracing started.
		// eventually we'll make it so those goroutines don't end up in stateReady, at which point we should remove
		// these entries.
		stateInactive:       true,
		stateBlocked:        true,
		stateBlockedSyscall: true,
	},
	stateReady: {
		stateActive:       true,
		stateGCMarkAssist: true,
		stateGCIdle:       true,
		stateGCDedicated:  true,
	},
	stateBlocked:                 {stateReady: true},
	stateBlockedSend:             {stateReady: true},
	stateBlockedRecv:             {stateReady: true},
	stateBlockedSelect:           {stateReady: true},
	stateBlockedSync:             {stateReady: true},
	stateBlockedSyncOnce:         {stateReady: true},
	stateBlockedSyncTriggeringGC: {stateReady: true},
	stateBlockedCond:             {stateReady: true},
	stateBlockedNet:              {stateReady: true},
	stateBlockedGC:               {stateReady: true},
	stateBlockedSyscall: {
		stateReady: true,
	},

	stateGCMarkAssist: {
		// active -> ready occurs on preemption
		stateReady:       true,
		stateActive:      true, // back to the goroutine's previous state
		stateInactive:    true, // mark assist can be preempted
		stateBlocked:     true,
		stateBlockedSync: true,
		stateBlockedGC:   true, // XXX what does this transition mean?
	},

	stateGCSweep: {
		stateActive: true, // back to the goroutine's previous state
	},
}

type Trace struct {
	gs  []*Goroutine
	ps  []*Processor
	gc  Spans
	stw Spans
	trace.ParseResult
}

//gcassert:inline
func (t *Trace) Reason(s *Span) reason {
	return reasonByEventType[t.Events[s.event()].Type]
}

//gcassert:inline
func (t *Trace) Event(ev EventID) *trace.Event {
	return &t.Events[ev]
}

//gcassert:inline
func (t *Trace) Duration(s *Span) time.Duration {
	return time.Duration(s.end - t.Event(s.event()).Ts)
}

func (tr *Trace) getG(gid uint64) *Goroutine {
	idx, found := sort.Find(len(tr.gs), func(idx int) int {
		ogid := tr.gs[idx].id
		if gid > ogid {
			return 1
		} else if gid == ogid {
			return 0
		} else {
			return -1
		}
	})
	if !found {
		panic(fmt.Sprintf("couldn't find goroutine %d", gid))
	}
	return tr.gs[idx]
}

// Spans represents a list of consecutive spans from a shared timeline.
type Spans []Span

func (ms MergedSpans) Start(tr *Trace) trace.Timestamp           { return Spans(ms).Start(tr) }
func (ms MergedSpans) End() trace.Timestamp                      { return Spans(ms).End() }
func (ms MergedSpans) Duration(tr *Trace) time.Duration          { return Spans(ms).Duration(tr) }
func (ms MergedSpans) Events(all []EventID, tr *Trace) []EventID { return Spans(ms).Events(all, tr) }

func (spans Spans) Start(tr *Trace) trace.Timestamp {
	return tr.Events[spans[0].event()].Ts
}

func (spans Spans) End() trace.Timestamp {
	return spans[len(spans)-1].end
}

func (spans Spans) Duration(tr *Trace) time.Duration {
	return time.Duration(spans.End() - spans.Start(tr))
}

func (spans Spans) Events(all []EventID, tr *Trace) []EventID {
	if len(all) == 0 {
		return nil
	}

	end := sort.Search(len(all), func(i int) bool {
		ev := all[i]
		return tr.Event(ev).Ts >= spans.End()
	})

	sTs := spans.Start(tr)

	start := sort.Search(len(all[:end]), func(i int) bool {
		ev := all[i]
		return tr.Event(ev).Ts >= sTs
	})

	return all[start:end]
}

type Span struct {
	// We track the end time, instead of looking at the next span's start time, because per-P timelines can have gaps,
	// and filling those gaps would probably use more memory than tracking the end time.
	end    trace.Timestamp
	event_ [5]byte
	// at is an offset from the top of the stack, skipping over uninteresting runtime frames.
	at uint8
	// We track the scheduling state explicitly, instead of mapping from trace.Event.Type, because we apply pattern
	// matching to stack traces that may result in more accurate states. For example, we can determine
	// stateBlockedSyncOnce from the stack trace, and we would otherwise use stateBlockedSync.
	state schedulingState
	tags  spanTags
}

func (s *Span) Events(all []EventID, tr *Trace) []EventID {
	// TODO(dh): this code is virtually identical to the code in MergedSpans.Events, but we cannot reuse that without
	// allocating.

	if len(all) == 0 {
		return nil
	}

	// AllEvents returns all events in the span's container (a goroutine), sorted by timestamp, as indices into the
	// global list of events. Find the first and last event that overlaps with the span, and that is the set of events
	// belonging to this span.

	end := sort.Search(len(all), func(i int) bool {
		ev := all[i]
		return tr.Event(ev).Ts >= s.end
	})

	sTs := tr.Event(s.event()).Ts

	start := sort.Search(len(all[:end]), func(i int) bool {
		ev := all[i]
		return tr.Event(ev).Ts >= sTs
	})

	return all[start:end]
}

//gcassert:inline
func (s *Span) event() EventID {
	return EventID(s.event_[0]) |
		EventID(s.event_[1])<<8 |
		EventID(s.event_[2])<<16 |
		EventID(s.event_[3])<<24 |
		EventID(s.event_[4])<<32
}

func fromUint40(n *[5]byte) int {
	if *n == ([5]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}) {
		return -1
	}

	return int(uint64(n[0]) |
		uint64(n[1])<<8 |
		uint64(n[2])<<16 |
		uint64(n[3])<<24 |
		uint64(n[4])<<32)
}

//gcassert:inline
func packEventID(id EventID) [5]byte {
	if debug && id >= 1<<40 {
		panic(fmt.Sprintf("id %d doesn't fit in uint40", id))
	}

	return [5]byte{
		byte(id),
		byte(id >> 8),
		byte(id >> 16),
		byte(id >> 24),
		byte(id >> 32),
	}
}

type Processor struct {
	id int32
	// OPT(dh): using Span for Ps is wasteful. We don't need tags, stacktrace offsets etc. We only care about what
	// goroutine is running at what time. The only benefit of reusing Span is that we can use the same code for
	// rendering Gs and Ps, but that doesn't seem worth the added cost.
	spans Spans
}

// XXX goroutine 0 seems to be special and doesn't get (un)scheduled. look into that.

type Goroutine struct {
	id       uint64
	function string
	spans    Spans
	events   []EventID
}

func (g *Goroutine) AllEvents() []EventID {
	return g.events
}

func (g *Goroutine) String() string {
	// OPT(dh): cache this. especially because it gets called a lot by the goroutine selector window.
	if g.function == "" {
		// At least GCSweepStart can happen on g0
		return local.Sprintf("goroutine %d", g.id)
	} else {
		return local.Sprintf("goroutine %d: %s", g.id, g.function)
	}
}

func loadTrace(path string, ch chan Command) (*Trace, error) {
	const ourStages = 1
	const totalStages = trace.Stages + ourStages

	var gs []*Goroutine
	var ps []*Processor
	var gc Spans
	var stw Spans

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	p, err := trace.NewParser(f)
	if err != nil {
		return nil, err
	}
	p.Progress = func(stage, cur, total int) {
		progress := (float32(cur) / float32(total)) / totalStages
		progress += (1.0 / totalStages) * float32(stage)

		ch <- Command{"setProgress", progress}
	}
	res, err := p.Parse()
	if err != nil {
		return nil, err
	}

	if exitAfterParsing {
		return nil, errExitAfterParsing
	}

	var evTypeToState = [...]schedulingState{
		trace.EvGoBlockSend:   stateBlockedSend,
		trace.EvGoBlockRecv:   stateBlockedRecv,
		trace.EvGoBlockSelect: stateBlockedSelect,
		trace.EvGoBlockSync:   stateBlockedSync,
		trace.EvGoBlockCond:   stateBlockedCond,
		trace.EvGoBlockNet:    stateBlockedNet,
		trace.EvGoBlockGC:     stateBlockedGC,
		trace.EvGoBlock:       stateBlocked,
	}

	gsByID := map[uint64]*Goroutine{}
	getG := func(gid uint64) *Goroutine {
		g, ok := gsByID[gid]
		if ok {
			return g
		}
		g = &Goroutine{id: gid}
		gsByID[gid] = g
		return g
	}
	psByID := map[int32]*Processor{}
	getP := func(pid int32) *Processor {
		p, ok := psByID[pid]
		if ok {
			return p
		}
		p = &Processor{id: pid}
		psByID[pid] = p
		return p
	}

	lastSyscall := map[uint64]uint32{}
	inMarkAssist := map[uint64]struct{}{}

	// FIXME(dh): rename function. or remove it alright
	addEventToCurrentSpan := func(gid uint64, ev EventID) {
		if gid == 0 {
			// FIXME(dh): figure out why we have events for g0 when there are no spans on g0.
			return
		}
		g := getG(gid)
		g.events = append(g.events, ev)
	}

	// Count the number of events per goroutine to get an estimate of spans per goroutine, to preallocate slices.
	eventsPerG := map[uint64]int{}
	eventsPerP := map[int32]int{}
	for evID := range res.Events {
		ev := &res.Events[evID]
		var gid uint64
		switch ev.Type {
		case trace.EvGoCreate, trace.EvGoUnblock:
			gid = ev.Args[0]
		case trace.EvGoStart, trace.EvGoStartLabel:
			eventsPerP[ev.P]++
			gid = ev.G
		case trace.EvGCStart, trace.EvGCSTWStart, trace.EvGCDone, trace.EvGCSTWDone,
			trace.EvHeapAlloc, trace.EvHeapGoal, trace.EvGomaxprocs, trace.EvUserTaskCreate,
			trace.EvUserTaskEnd, trace.EvUserRegion, trace.EvUserLog, trace.EvCPUSample,
			trace.EvProcStart, trace.EvProcStop, trace.EvGoSysCall:
			continue
		default:
			gid = ev.G
		}
		eventsPerG[gid]++
	}
	for gid, n := range eventsPerG {
		getG(gid).spans = make(Spans, 0, n)
	}
	for pid, n := range eventsPerP {
		getP(pid).spans = make(Spans, 0, n)
	}

	for evID := range res.Events {
		ev := &res.Events[evID]
		if evID%10000 == 0 {
			select {
			case ch <- Command{"setProgress", ((1.0 / totalStages) * (trace.Stages + 0)) + (float32(evID)/float32(len(res.Events)))/totalStages}:
			default:
				// Don't let the rendering loop slow down parsing. Especially when vsync is enabled we'll only get to
				// read commands every blanking interval.
			}
		}
		var gid uint64
		var state schedulingState
		var pState int

		const (
			pNone = iota
			pRunG
			pStopG
		)

		switch ev.Type {
		case trace.EvGoCreate:
			// ev.G creates ev.Args[0]
			if ev.G != 0 {
				addEventToCurrentSpan(ev.G, EventID(evID))
			}
			gid = ev.Args[0]
			if ev.Args[1] != 0 {
				stack := res.Stacks[uint32(ev.Args[1])]
				if len(stack) != 0 {
					getG(gid).function = res.PCs[stack[0]].Fn
				}
			}
			// FIXME(dh): when tracing starts after goroutines have already been created then we receive an EvGoCreate
			// for them. But those goroutines may not necessarily be in a non-running state. We do receive EvGoWaiting
			// and EvGoInSyscall for goroutines that are blocked or in a syscall when tracing starts; does that mean
			// that any goroutine that doesn't receive this event is currently running? If so we'd have to detect which
			// goroutines receive neither EvGoWaiting or EvGoInSyscall, and which were already running.
			//
			// EvGoWaiting is emitted when we're in _Gwaiting, and EvGoInSyscall when we're in _Gsyscall. Critically
			// this doesn't cover _Gidle and _Grunnable, which means we don't know if it's running or waiting to run. If
			// there's another event then we can deduce it (we can't go from _Grunnable to _Gblocked, for example), but
			// if there are no more events, then we cannot tell if the goroutine was always running or always runnable.
			state = stateCreated
		case trace.EvGoStart:
			// ev.G starts running
			gid = ev.G
			pState = pRunG

			if _, ok := inMarkAssist[gid]; ok {
				state = stateGCMarkAssist
			} else {
				state = stateActive
			}
		case trace.EvGoStartLabel:
			// ev.G starts running
			// TODO(dh): make use of the label
			gid = ev.G
			pState = pRunG
			state = stateActive

			switch res.Strings[ev.Args[2]] {
			case "GC (dedicated)":
				state = stateGCDedicated
			case "GC (idle)":
				state = stateGCIdle
			}
		case trace.EvGoStop:
			// ev.G is stopping
			gid = ev.G
			pState = pStopG
			state = stateStuck
		case trace.EvGoEnd:
			// ev.G is ending
			gid = ev.G
			pState = pStopG
			state = stateDone
		case trace.EvGoSched:
			// ev.G calls Gosched
			gid = ev.G
			pState = pStopG
			state = stateInactive
		case trace.EvGoSleep:
			// ev.G calls Sleep
			gid = ev.G
			pState = pStopG
			state = stateInactive
		case trace.EvGoPreempt:
			// ev.G got preempted
			gid = ev.G
			pState = pStopG
			state = stateReady
		case trace.EvGoBlockSend, trace.EvGoBlockRecv, trace.EvGoBlockSelect,
			trace.EvGoBlockSync, trace.EvGoBlockCond, trace.EvGoBlockNet,
			trace.EvGoBlockGC:
			// ev.G is blocking
			gid = ev.G
			pState = pStopG
			state = evTypeToState[ev.Type]
		case trace.EvGoBlock:
			// ev.G is blocking
			gid = ev.G
			pState = pStopG
			state = evTypeToState[ev.Type]

			if ev.Type == trace.EvGoBlock {
				if blockedIsInactive(gsByID[gid].function) {
					state = stateInactive
				}
			}
		case trace.EvGoWaiting:
			// ev.G is blocked when tracing starts
			gid = ev.G
			state = stateBlocked
			if blockedIsInactive(gsByID[gid].function) {
				state = stateInactive
			}
		case trace.EvGoUnblock:
			// ev.G is unblocking ev.Args[0]
			addEventToCurrentSpan(ev.G, EventID(evID))
			gid = ev.Args[0]
			state = stateReady
		case trace.EvGoSysCall:
			// From the runtime's documentation:
			//
			// Syscall tracing:
			// At the start of a syscall we emit traceGoSysCall to capture the stack trace.
			// If the syscall does not block, that is it, we do not emit any other events.
			// If the syscall blocks (that is, P is retaken), retaker emits traceGoSysBlock;
			// when syscall returns we emit traceGoSysExit and when the goroutine starts running
			// (potentially instantly, if exitsyscallfast returns true) we emit traceGoStart.

			// XXX guard against malformed trace
			lastSyscall[ev.G] = ev.StkID
			addEventToCurrentSpan(ev.G, EventID(evID))
			continue
		case trace.EvGoSysBlock:
			gid = ev.G
			pState = pStopG
			state = stateBlockedSyscall
		case trace.EvGoInSyscall:
			gid = ev.G
			state = stateBlockedSyscall
		case trace.EvGoSysExit:
			gid = ev.G
			state = stateReady
		case trace.EvProcStart, trace.EvProcStop:
			// TODO(dh): should we implement a per-M timeline that shows which procs are running on which OS threads?
			continue

		case trace.EvGCMarkAssistStart:
			// User goroutines may be asked to assist the GC's mark phase. This happens when the goroutine allocates
			// memory and some condition is true. When that happens, the tracer emits EvGCMarkAssistStart for that
			// goroutine.
			//
			// Note that this event is not preceeded by an EvGoBlock or similar. Similarly, EvGCMarkAssistDone is not
			// succeeded by an EvGoStart or similar. The mark assist events are laid over the normal goroutine
			// scheduling events.
			//
			// We instead turn these into proper goroutine states and split the current span in two to make room for
			// mark assist. This needs special care because mark assist can be preempted, so we might GoStart into mark
			// assist.

			gid = ev.G
			state = stateGCMarkAssist
			inMarkAssist[gid] = struct{}{}
		case trace.EvGCMarkAssistDone:
			// The counterpart to EvGCMarkAssistStop.

			gid = ev.G
			state = stateActive
			delete(inMarkAssist, gid)
		case trace.EvGCSweepStart:
			// This is similar to mark assist, but for sweeping spans. When a goroutine would need to allocate a new
			// span, it first sweeps other spans of the same size to find a free one.
			//
			// Unlike mark assist, sweeping cannot be preempted, simplifying our state tracking.

			gid = ev.G
			state = stateGCSweep
		case trace.EvGCSweepDone:
			// The counterpart to EvGcSweepStart.

			// XXX apparently this can happen on g0, in which case going to stateActive is probably wrong.
			gid = ev.G
			state = stateActive

		case trace.EvGCStart:
			gc = append(gc, Span{state: stateActive, event_: packEventID(EventID(evID))})
			continue

		case trace.EvGCSTWStart:
			stw = append(stw, Span{state: stateActive, event_: packEventID(EventID(evID))})
			continue

		case trace.EvGCDone:
			// XXX verify that index isn't out of bounds
			gc[len(gc)-1].end = ev.Ts
			continue

		case trace.EvGCSTWDone:
			// Even though STW happens as part of GC, we can see EvGCSTWDone after EvGCDone.
			// XXX verify that index isn't out of bounds
			stw[len(stw)-1].end = ev.Ts
			continue

		case trace.EvHeapAlloc:
			// Instant measurement of currently allocated memory
			continue
		case trace.EvHeapGoal:
			// Instant measurement of new heap goal

			// TODO(dh): implement
			continue

		case trace.EvGomaxprocs:
			// TODO(dh): graph GOMAXPROCS
			continue
		case trace.EvUserTaskCreate, trace.EvUserTaskEnd, trace.EvUserRegion:
			// TODO(dh): implement a per-task timeline
			// TODO(dh): incorporate regions and logs in per-goroutine timeline
			continue

		case trace.EvUserLog:
			addEventToCurrentSpan(ev.G, EventID(evID))
			continue

		case trace.EvCPUSample:
			// XXX make use of CPU samples
			continue

		default:
			return nil, fmt.Errorf("unsupported trace event %d", ev.Type)
		}

		if debug {
			if s := getG(gid).spans; len(s) > 0 {
				if len(s) == 1 && ev.Type == trace.EvGoWaiting && s[0].state == stateInactive {
					// The execution trace emits GoCreate + GoWaiting for goroutines that already exist at the start of
					// tracing if they're in a blocked state. This causes a transition from inactive to blocked, which we
					// wouldn't normally permit.
				} else {
					prevState := s[len(s)-1].state
					if !legalStateTransitions[prevState][state] {
						panic(fmt.Sprintf("illegal state transition %d -> %d for goroutine %d, time %d", prevState, state, gid, ev.Ts))
					}
				}
			}
		}

		s := Span{state: state, event_: packEventID(EventID(evID))}
		if ev.Type == trace.EvGoSysBlock {
			if debug && res.Events[s.event()].StkID != 0 {
				panic("expected zero stack ID")
			}
			res.Events[s.event()].StkID = lastSyscall[ev.G]
		}

		getG(gid).spans = append(getG(gid).spans, s)

		switch pState {
		case pRunG:
			p := getP(ev.P)
			p.spans = append(p.spans, Span{state: stateRunningG, event_: packEventID(EventID(evID))})
		case pStopG:
			// XXX guard against malformed traces
			p := getP(ev.P)
			p.spans[len(p.spans)-1].end = ev.Ts
		}
	}

	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for _, g := range gsByID {
		sem <- struct{}{}
		g := g
		wg.Add(1)
		go func() {
			for i, s := range g.spans {
				if i != len(g.spans)-1 {
					s.end = res.Events[g.spans[i+1].event()].Ts
				}

				stack := res.Stacks[res.Events[s.event()].StkID]
				s = applyPatterns(s, res.PCs, stack)

				// move s.At out of the runtime
				for int(s.at+1) < len(stack) && s.at < 255 && strings.HasPrefix(res.PCs[stack[s.at]].Fn, "runtime.") {
					s.at++
				}

				g.spans[i] = s
			}

			if len(g.spans) != 0 {
				last := g.spans[len(g.spans)-1]
				if last.state == stateDone {
					// The goroutine has ended
					// XXX the event probably has a stack associated with it, which we shouldn't discard.
					g.spans = g.spans[:len(g.spans)-1]
				} else {
					// XXX somehow encode open-ended traces
					g.spans[len(g.spans)-1].end = res.Events[len(res.Events)-1].Ts
				}
			}

			<-sem
			wg.Done()
		}()
	}
	wg.Wait()

	// Note: There is no point populating gs and ps in parallel, because ps only contains a handful of items.
	for _, g := range gsByID {
		if len(g.spans) != 0 {
			// OPT(dh): preallocate gs
			gs = append(gs, g)
		}
	}

	sort.Slice(gs, func(i, j int) bool {
		return gs[i].id < gs[j].id
	})

	for _, p := range psByID {
		// OPT(dh): preallocate ps
		ps = append(ps, p)
	}

	sort.Slice(ps, func(i, j int) bool {
		return ps[i].id < ps[j].id
	})

	if exitAfterLoading {
		return nil, errExitAfterLoading
	}

	return &Trace{gs: gs, ps: ps, gc: gc, stw: stw, ParseResult: res}, nil
}

// Several background goroutines in the runtime go into a blocked state when they have no work to do. In all cases, this
// is more similar to a goroutine calling runtime.Gosched than to a goroutine really wishing it had work to do. Because
// of that we put those into the inactive state.
func blockedIsInactive(fn string) bool {
	if fn == "" {
		return false
	}
	switch fn {
	case "runtime.gcBgMarkWorker", "runtime.forcegchelper", "runtime.bgsweep", "runtime.bgscavenge", "runtime.runfinq":
		return true
	default:
		return false
	}
}