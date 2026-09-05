'use client';
import { useState, useRef, useLayoutEffect } from 'react';
import {
  Layers,
  House,
  Inbox,
  Plus,
  Search,
  ChevronRight,
  ChevronDown,
  ArrowUp,
  ArrowUpRight,
  ArrowLeft,
  PanelRight,
  PanelLeft,
  MessageSquare,
  Terminal,
  Workflow,
  ListTodo,
  X,
  Check,
  AlertTriangle,
  Clock,
  Copy,
  RefreshCw,
  Pause,
  Play,
  GitBranch,
  ArrowRight,
  Activity,
  FileText,
  Minus,
  Maximize2,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog';
import {
  seedSessions,
  seedEvents,
  ancestors,
  eventTarget,
  escalation,
  freshSession,
  type SessionRecord,
  type EventRecord,
  type NodeRecord,
  type TaskRecord,
} from './workbench-data';
import './workbench.css';
type View = 'conversation' | 'tasks' | 'graph' | 'terminal';
type Selection =
  | { kind: 'session' }
  | { kind: 'task'; id: string }
  | { kind: 'node'; id: string };
type SessionUI = { tab: View; draft: string; detail: Selection | null };
const blank: SessionUI = { tab: 'conversation', draft: '', detail: null };
const tabNames: Record<View, string> = {
  conversation: 'Conversation',
  tasks: 'Tasks',
  graph: 'Graph',
  terminal: 'Terminal',
};
function Badge({ value }: { value: string }) {
  return (
    <span className={'wb-badge ' + value}>
      <i />
      {value}
    </span>
  );
}
function Resource({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="wb-resource">
      {value ? (
        /^https?:\/\//.test(value) ? (
          <a href={value} target="_blank" rel="noreferrer">
            <ArrowUpRight size={14} />
            {value.replace(/^https?:\/\//, '')}
          </a>
        ) : (
          <code>{value}</code>
        )
      ) : (
        <span>No resource</span>
      )}
      {value && (
        <button
          aria-label="Copy resource identifier"
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(value);
              setCopied(true);
            } catch {
              setCopied(false);
            }
          }}
        >
          {copied ? <Check size={13} /> : <Copy size={13} />}
        </button>
      )}
    </div>
  );
}
function RecordValues({
  title,
  value,
}: {
  title: string;
  value: Record<string, unknown>;
}) {
  return (
    <section className="wb-record">
      <h3>{title}</h3>
      {Object.keys(value).length ? (
        <dl>
          {Object.entries(value).map(([k, v]) => (
            <div key={k}>
              <dt>{k}</dt>
              <dd>
                {typeof v === 'object' ? (
                  <details>
                    <summary>{Array.isArray(v) ? 'Array' : 'Object'}</summary>
                    <pre>{JSON.stringify(v, null, 2)}</pre>
                  </details>
                ) : (
                  String(v)
                )}
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <p>No recorded values</p>
      )}
    </section>
  );
}
export default function Workbench() {
  const [sessions, setSessions] = useState(seedSessions),
    [events, setEvents] = useState(seedEvents),
    [active, setActive] = useState('config'),
    [page, setPage] = useState<'session' | 'home' | 'inbox'>('session'),
    [expanded, setExpanded] = useState<string[]>(['release', 'config', 'docs']),
    [query, setQuery] = useState(''),
    [ui, setUI] = useState<Record<string, SessionUI>>({}),
    [sidebar, setSidebar] = useState(true),
    [create, setCreate] = useState(false),
    [name, setName] = useState(''),
    [resource, setResource] = useState(''),
    [scope, setScope] = useState('all'),
    [eventFilter, setEventFilter] = useState('all'),
    [zoom, setZoom] = useState(1),
    [fetched, setFetched] = useState<Record<string, string>>({}),
    [fetching, setFetching] = useState(false),
    [notice, setNotice] = useState(''),
    [highlight, setHighlight] = useState('');
  const scroll = useRef<HTMLDivElement>(null),
    positions = useRef<Record<string, number>>({});
  const current = sessions.find((s) => s.id === active)!;
  const state = ui[active] ?? blank;
  const location = page === 'session' ? active + ':' + state.tab : page;
  const open = page === 'session' && !!state.detail;
  function patch(p: Partial<SessionUI>) {
    setUI((v) => ({ ...v, [active]: { ...(v[active] ?? blank), ...p } }));
  }
  useLayoutEffect(() => {
    if (scroll.current)
      scroll.current.scrollTop = positions.current[location] ?? 0;
  }, [location]);
  function select(id: string, detail?: Selection, event?: string) {
    setActive(id);
    if (window.matchMedia('(max-width:700px)').matches) setSidebar(false);
    setPage('session');
    setExpanded((v) => Array.from(new Set([...v, ...ancestors(sessions, id)])));
    if (detail || event)
      setUI((v) => ({
        ...v,
        [id]: {
          ...(v[id] ?? blank),
          ...(detail ? { detail } : {}),
          ...(event ? { tab: 'conversation' as View } : {}),
        },
      }));
    setHighlight(event ?? '');
    if (event) {
      setTimeout(
        () =>
          document
            .getElementById('event-' + event)
            ?.scrollIntoView({ block: 'center' }),
        80,
      );
    }
  }
  function navigate(p: 'home' | 'inbox') {
    setPage(p);
    setHighlight('');
    setNotice('');
  }
  function send() {
    if (!state.draft.trim()) return;
    const e: EventRecord = {
      id: 'local-' + Date.now(),
      session: active,
      time: new Date().toLocaleTimeString('en-GB'),
      type: 'user.emit',
      source: 'web',
      body: state.draft.trim(),
    };
    setEvents((v) => [...v, e]);
    patch({ draft: '' });
    setNotice('Added locally · No live delivery');
    setTimeout(
      () => scroll.current?.scrollTo({ top: scroll.current.scrollHeight }),
      40,
    );
  }
  function runtime() {
    setNotice(
      `Mock only · plect ${current.run === 'up' ? 'down' : 'up'} ${active} was not executed`,
    );
  }
  function start() {
    if (!name.trim() || !resource.trim()) return;
    const id = name.trim();
    if (sessions.some((session) => session.id === id)) {
      setNotice('Session name already exists');
      return;
    }
    setSessions((v) => [...v, freshSession(id, resource.trim())]);
    setActive(id);
    if (window.matchMedia('(max-width:700px)').matches) setSidebar(false);
    setPage('session');
    setCreate(false);
    setName('');
    setResource('');
    setNotice('Local session created · No process started');
  }
  const matches = sessions.filter((s) =>
    (s.id + ' ' + s.resource).toLowerCase().includes(query.toLowerCase()),
  );
  const visibleIds = new Set(
    matches.flatMap((s) => [s.id, ...ancestors(sessions, s.id)]),
  );
  function tree(parent?: string, depth = 0): React.ReactNode {
    return sessions
      .filter((s) => s.parent === parent)
      .filter((s) => !query || visibleIds.has(s.id))
      .map((s) => {
        const children = sessions.some((x) => x.parent === s.id),
          isExpanded = query ? true : expanded.includes(s.id);
        return (
          <div
            key={s.id}
            role="treeitem"
            aria-level={depth + 1}
            aria-expanded={children ? isExpanded : undefined}
            aria-selected={page === 'session' && active === s.id}
          >
            <div
              className={
                'wb-tree-row ' +
                (page === 'session' && active === s.id ? 'selected' : '')
              }
              style={{ paddingLeft: 12 + depth * 17 }}
            >
              <button
                className="wb-expander"
                aria-label={(isExpanded ? 'Collapse ' : 'Expand ') + s.id}
                disabled={!children}
                onClick={() =>
                  setExpanded((v) =>
                    v.includes(s.id)
                      ? v.filter((x) => x !== s.id)
                      : [...v, s.id],
                  )
                }
              >
                {children ? (
                  isExpanded ? (
                    <ChevronDown size={14} />
                  ) : (
                    <ChevronRight size={14} />
                  )
                ) : (
                  <span className="wb-tree-leaf" />
                )}
              </button>
              <button
                className="wb-tree-select"
                onClick={() => select(s.id)}
                title={s.id}
              >
                <span>{s.id}</span>
                <i
                  className={
                    'wb-tree-dot ' +
                    (s.health === 'unhealthy' ? 'failed' : s.run)
                  }
                  aria-label={s.health === 'unhealthy' ? 'unhealthy' : s.run}
                />
              </button>
            </div>
            {children && isExpanded && (
              <div role="group">{tree(s.id, depth + 1)}</div>
            )}
          </div>
        );
      });
  }
  function eventRow(e: EventRecord, global = false) {
    const isUser = e.type === 'user.emit',
      instruction = e.type === 'plect.instruction',
      status = e.type === 'plect.status_message',
      important = escalation(e);
    const owner = sessions.find((s) => s.id === eventTarget(e));
    return (
      <article
        id={'event-' + e.id}
        key={e.id}
        className={
          'wb-event ' +
          (isUser || instruction ? 'message' : 'compact') +
          (important ? ' escalation' : '') +
          (highlight === e.id ? ' highlighted' : '')
        }
      >
        <div
          className={
            'wb-event-marker ' + (isUser ? 'user' : important ? 'error' : '')
          }
        >
          {isUser ? (
            'Y'
          ) : instruction ? (
            <FileText size={16} />
          ) : important ? (
            <AlertTriangle size={15} />
          ) : e.type.endsWith('.done') ? (
            <Check size={15} />
          ) : status ? (
            <Activity size={15} />
          ) : (
            <GitBranch size={15} />
          )}
        </div>
        <div className="wb-event-content">
          {global && (
            <button
              className="wb-event-session"
              onClick={() => select(e.session, undefined, e.id)}
            >
              {ancestors(sessions, e.session)
                .map((id) => sessions.find((s) => s.id === id)?.id)
                .join(' / ')}
              {ancestors(sessions, e.session).length ? ' / ' : ''}
              {sessions.find((s) => s.id === e.session)?.id}
              <ArrowUpRight size={12} />
            </button>
          )}
          <div className="wb-event-title">
            <strong>
              {isUser
                ? 'You'
                : instruction
                  ? 'Task instruction'
                  : status
                    ? 'Status update'
                    : important
                      ? 'Escalation'
                      : e.type.endsWith('.done')
                        ? 'Task conditions satisfied'
                        : 'Session created'}
            </strong>
            <time>{e.time}</time>
          </div>
          <p>{e.body}</p>
          {e.origin && (
            <div className="wb-origin">
              From{' '}
              <button onClick={() => select(e.origin!)}>
                {owner?.id ?? e.origin}
                <ArrowUpRight size={12} />
              </button>
              <span>→ {sessions.find((s) => s.id === e.session)?.id}</span>
            </div>
          )}
          <div className="wb-event-links">
            {e.task && (
              <button
                onClick={() => select(e.session, { kind: 'task', id: e.task! })}
              >
                <ListTodo size={13} />
                {e.task}
                <ChevronRight size={12} />
              </button>
            )}
            {e.node && (
              <button
                onClick={() => {
                  select(e.session, { kind: 'node', id: e.node! });
                  setUI((v) => ({
                    ...v,
                    [e.session]: {
                      ...(v[e.session] ?? blank),
                      tab: 'graph',
                      detail: { kind: 'node', id: e.node! },
                    },
                  }));
                }}
              >
                <Workflow size={13} />
                {e.node}
                <ChevronRight size={12} />
              </button>
            )}
            {e.related && (
              <button onClick={() => select(e.related!)}>
                <GitBranch size={13} />
                Open session
                <ArrowUpRight size={12} />
              </button>
            )}
            <details>
              <summary>Event</summary>
              <code>
                {e.type} · {e.source}
                <br />
                recorded in {e.session}
                {e.origin ? (
                  <>
                    <br />
                    origin {e.origin}
                  </>
                ) : null}
              </code>
            </details>
          </div>
        </div>
      </article>
    );
  }
  const selectedTask =
    state.detail?.kind === 'task'
      ? current.tasks.find(
          (t) =>
            t.id === (state.detail?.kind === 'task' ? state.detail.id : ''),
        )
      : undefined;
  const selectedNode =
    state.detail?.kind === 'node'
      ? current.nodes.find(
          (n) =>
            n.id === (state.detail?.kind === 'node' ? state.detail.id : ''),
        )
      : undefined;
  const filtered = events
    .filter((e) =>
      page === 'session'
        ? e.session === active
        : scope === 'all' || e.session === scope,
    )
    .filter((e) =>
      page === 'inbox'
        ? escalation(e)
        : eventFilter === 'all' || e.type === eventFilter,
    );
  return (
    <div
      className={
        'wb ' +
        (sidebar ? '' : 'wb-no-sidebar') +
        (open ? ' wb-has-detail' : '')
      }
    >
      {sidebar && (
        <aside className="wb-sidebar">
          <div className="wb-brand">
            <Layers size={25} />
            <strong>Plecture</strong>
            <button aria-label="Hide sidebar" onClick={() => setSidebar(false)}>
              <PanelLeft size={17} />
            </button>
          </div>
          <nav>
            <button
              className={page === 'home' ? 'active' : ''}
              onClick={() => navigate('home')}
            >
              <House size={18} />
              Home
            </button>
            <button
              className={page === 'inbox' ? 'active' : ''}
              onClick={() => navigate('inbox')}
            >
              <Inbox size={18} />
              Inbox
            </button>
          </nav>
          <div className="wb-sidebar-heading">
            <span>SESSIONS</span>
            <button aria-label="New session" onClick={() => setCreate(true)}>
              <Plus size={17} />
            </button>
          </div>
          <div className="wb-search">
            <Search size={14} />
            <Input
              aria-label="Search sessions"
              value={query}
              placeholder="Find a session…"
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <div className="wb-tree" role="tree" aria-label="Session hierarchy">
            {tree()}
            {query && !matches.length && (
              <p className="wb-empty-small">No matching sessions</p>
            )}
          </div>
          <footer>
            <span>
              <i />
              {sessions.filter((s) => s.run === 'up').length} up ·{' '}
              {sessions.length} sessions
            </span>
            <span>MOCK</span>
          </footer>
        </aside>
      )}
      <main className="wb-main">
        <header
          className={`wb-header ${page === 'session' ? 'wb-session-header' : ''}`}
        >
          <div className="wb-title-line">
            {!sidebar && (
              <button
                aria-label="Show sidebar"
                onClick={() => setSidebar(true)}
              >
                <PanelLeft size={18} />
              </button>
            )}
            <div>
              {page === 'session' ? (
                <h1>{current.id}</h1>
              ) : (
                <>
                  <span className="wb-ancestry">WORKSPACE</span>
                  <h1>{page === 'home' ? 'Home' : 'Inbox'}</h1>
                </>
              )}
            </div>
          </div>
          <div className="wb-header-actions">
            {page === 'session' ? (
              <>
                <Badge value={current.run} />
                <Button variant="ghost" size="sm" onClick={runtime}>
                  {current.run === 'up' ? (
                    <Pause size={14} />
                  ) : (
                    <Play size={14} />
                  )}{' '}
                  {current.run === 'up' ? 'Down' : 'Up'}
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label="Session details"
                  onClick={() =>
                    patch({
                      detail:
                        state.detail?.kind === 'session'
                          ? null
                          : { kind: 'session' },
                    })
                  }
                >
                  <PanelRight size={18} />
                </Button>
              </>
            ) : (
              <span className="wb-muted">Mock snapshot · Sep 5</span>
            )}
          </div>
        </header>
        {page === 'session' ? (
          <>
            <div className="wb-context">
              <Resource value={current.resource} />
              {current.health === 'unhealthy' && <Badge value="unhealthy" />}
            </div>
            <Tabs
              value={state.tab}
              onValueChange={(v) => patch({ tab: v as View })}
            >
              <TabsList className="wb-tabs" variant="line">
                {(
                  [
                    ['conversation', MessageSquare],
                    ['tasks', ListTodo],
                    ['graph', Workflow],
                    ['terminal', Terminal],
                  ] as const
                ).map(([v, Icon]) => (
                  <TabsTrigger value={v} key={v}>
                    <Icon size={15} />
                    {tabNames[v]}
                    {v === 'tasks' && <span>{current.tasks.length}</span>}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
          </>
        ) : (
          <div className="wb-global-toolbar">
            <div>
              <h2>{page === 'home' ? 'Recent activity' : 'Escalations'}</h2>
              <span>
                {page === 'home'
                  ? 'All sessions · Recent recorded events'
                  : 'Recorded escalations · Resolution is not tracked'}
              </span>
            </div>
            <select
              aria-label="Filter by session"
              value={scope}
              onChange={(e) => setScope(e.target.value)}
            >
              <option value="all">All sessions</option>
              {sessions.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.id}
                </option>
              ))}
            </select>
            {page === 'home' && (
              <select
                aria-label="Filter event type"
                value={eventFilter}
                onChange={(e) => setEventFilter(e.target.value)}
              >
                <option value="all">All event types</option>
                {Array.from(new Set(events.map((e) => e.type))).map((t) => (
                  <option key={t}>{t}</option>
                ))}
              </select>
            )}
          </div>
        )}
        <div
          className={
            'wb-scroll ' + (page === 'session' ? 'view-' + state.tab : '')
          }
          ref={scroll}
          onScroll={(e) =>
            (positions.current[location] = e.currentTarget.scrollTop)
          }
        >
          {page !== 'session' ? (
            <div className="wb-timeline global">
              {[...filtered].reverse().map((e) => eventRow(e, true))}
              {!filtered.length && (
                <div className="wb-empty">
                  <Inbox size={28} />
                  <h2>No matching events</h2>
                  <p>Nothing recorded in this snapshot.</p>
                </div>
              )}
            </div>
          ) : state.tab === 'conversation' ? (
            <div className="wb-timeline">
              <div className="wb-date">
                TODAY <span>SEPTEMBER 5</span>
              </div>
              {filtered.map((e) => eventRow(e))}
              {!filtered.length && (
                <div className="wb-empty">
                  <MessageSquare size={28} />
                  <h2>No recorded events</h2>
                </div>
              )}
            </div>
          ) : state.tab === 'tasks' ? (
            <div className="wb-task-list">
              <div className="wb-section-heading">
                <h2>Tasks</h2>
                <span>
                  {
                    current.tasks.filter((t) => t.completion === 'satisfied')
                      .length
                  }{' '}
                  satisfied · {current.tasks.length} total
                </span>
              </div>
              {current.tasks.map((t) => (
                <button
                  className={
                    'wb-task-row ' +
                    (selectedTask?.id === t.id ? 'selected' : '')
                  }
                  key={t.id}
                  onClick={() => patch({ detail: { kind: 'task', id: t.id } })}
                >
                  <span className={'wb-task-icon ' + t.completion}>
                    {t.completion === 'satisfied' ? (
                      <Check size={19} />
                    ) : (
                      <Clock size={19} />
                    )}
                  </span>
                  <div>
                    <strong>{t.title}</strong>
                    <code>
                      {t.definition} / {t.id}
                    </code>
                  </div>
                  <Badge value={t.completion} />
                  <ChevronRight size={16} />
                </button>
              ))}
              {!current.tasks.length && (
                <div className="wb-empty">
                  <ListTodo size={28} />
                  <h2>No task instances</h2>
                  <p>No task has been recorded for this session.</p>
                </div>
              )}
            </div>
          ) : state.tab === 'graph' ? (
            <div className="wb-graph">
              <div className="wb-graph-toolbar">
                <span>
                  <Workflow size={16} />
                  {current.workflow}
                </span>
                <div>
                  <button
                    aria-label="Zoom out"
                    onClick={() => setZoom((z) => Math.max(0.6, z - 0.1))}
                  >
                    <Minus size={14} />
                  </button>
                  <span>{Math.round(zoom * 100)}%</span>
                  <button
                    aria-label="Zoom in"
                    onClick={() => setZoom((z) => Math.min(1.4, z + 0.1))}
                  >
                    <Plus size={14} />
                  </button>
                  <button
                    aria-label="Reset graph zoom"
                    onClick={() => setZoom(1)}
                  >
                    <Maximize2 size={14} />
                  </button>
                </div>
              </div>
              <div className="wb-graph-surface">
                {current.nodes.length ? (
                  <div
                    className="wb-graph-size"
                    style={{ width: 650 * zoom, height: 440 * zoom }}
                  >
                    <div
                      className="wb-graph-world"
                      style={{ transform: `scale(${zoom})` }}
                    >
                      <span className="wb-boundary-label">
                        {current.workflow} <small>workflow</small>
                      </span>
                      <svg
                        className="wb-edges"
                        width="650"
                        height="440"
                        aria-hidden="true"
                      >
                        <defs>
                          <marker
                            id="arrow"
                            viewBox="0 0 10 10"
                            refX="8"
                            refY="5"
                            markerWidth="6"
                            markerHeight="6"
                            orient="auto"
                          >
                            <path
                              d="M0 0 L10 5 L0 10"
                              fill="none"
                              stroke="#8096a0"
                              strokeWidth="2"
                            />
                          </marker>
                        </defs>
                        <path
                          d="M275 305 H365"
                          fill="none"
                          stroke="#8096a0"
                          strokeWidth="1.5"
                          markerEnd="url(#arrow)"
                        />
                      </svg>
                      <span className="wb-edge-label">queue_dir</span>
                      {current.nodes.map((n, i) => (
                        <button
                          key={n.id}
                          style={{
                            left: i === 2 ? 365 : 40,
                            top: i === 0 ? 60 : 240,
                          }}
                          className={
                            'wb-node ' +
                            n.status +
                            (selectedNode?.id === n.id ? ' selected' : '')
                          }
                          onClick={() =>
                            patch({ detail: { kind: 'node', id: n.id } })
                          }
                        >
                          <div>
                            <span>effect</span>
                            <Badge value={n.status} />
                          </div>
                          <strong>{n.id}</strong>
                          <code>{n.uses}</code>
                          <footer>
                            <span>{n.scope}</span>
                            {n.status === 'failed' ? (
                              <AlertTriangle size={14} />
                            ) : (
                              <ChevronRight size={14} />
                            )}
                          </footer>
                        </button>
                      ))}
                    </div>
                  </div>
                ) : (
                  <div className="wb-empty">
                    <Workflow size={28} />
                    <h2>No execution records</h2>
                  </div>
                )}
              </div>
              <div className="wb-graph-footer">
                <span>
                  <i />
                  Input binding
                </span>
                <span>Current configuration + stored records</span>
              </div>
              {current.nodes.some((n) => n.status === 'failed') && (
                <button
                  className="wb-failure"
                  onClick={() =>
                    patch({ detail: { kind: 'node', id: 'worker' } })
                  }
                >
                  <AlertTriangle size={17} />
                  <div>
                    <strong>worker failed</strong>
                    <span>Inspect the recorded error and inputs</span>
                  </div>
                  <ChevronRight size={16} />
                </button>
              )}
            </div>
          ) : (
            <div className="wb-terminal">
              <div className="wb-terminal-toolbar">
                <span>
                  <Terminal size={15} />
                  {current.id}
                </span>
                <div>
                  <span>
                    {fetched[active]
                      ? 'Fetched at ' + fetched[active]
                      : 'Mock snapshot · 14:16:00'}
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={
                      fetching || !current.capture || current.run === 'down'
                    }
                    onClick={() => {
                      const id = active;
                      setFetching(true);
                      setTimeout(() => {
                        setFetched((v) => ({
                          ...v,
                          [id]: new Date().toLocaleTimeString('en-GB'),
                        }));
                        setFetching(false);
                      }, 450);
                    }}
                  >
                    <RefreshCw
                      size={14}
                      className={fetching ? 'spinning' : ''}
                    />
                    Refresh
                  </Button>
                </div>
              </div>
              {current.capture ? (
                <>
                  <pre>{current.capture}</pre>
                  <div className="wb-capture-note">
                    {current.run === 'down'
                      ? 'Runtime down · Retained mock capture'
                      : 'Read-only capture · Refresh reloads the same mock content'}
                  </div>
                </>
              ) : (
                <div className="wb-empty">
                  <Terminal size={28} />
                  <h2>Capture unavailable</h2>
                  <p>
                    {current.nodes.length
                      ? 'Terminal runtime is not produced.'
                      : 'No terminal execution record.'}
                  </p>
                  <Button
                    variant="outline"
                    onClick={() => patch({ tab: 'graph' })}
                  >
                    Inspect graph
                  </Button>
                </div>
              )}
            </div>
          )}
        </div>
        {page === 'session' && state.tab === 'conversation' && (
          <div className="wb-composer-wrap">
            <form
              className="wb-composer"
              onSubmit={(e) => {
                e.preventDefault();
                send();
              }}
            >
              <Textarea
                aria-label="Message this session"
                value={state.draft}
                placeholder="Add instructions or share context…"
                onChange={(e) => patch({ draft: e.target.value })}
              />
              <div>
                <span>
                  <ArrowRight size={13} />
                  user.emit
                </span>
                <Button
                  type="submit"
                  size="icon-sm"
                  aria-label="Add message locally"
                  disabled={!state.draft.trim()}
                >
                  <ArrowUp size={17} />
                </Button>
              </div>
            </form>
            <div className="wb-composer-note">
              {notice || 'Mock workspace · No live delivery'}
            </div>
          </div>
        )}
        {notice && (page !== 'session' || state.tab !== 'conversation') && (
          <div className="wb-notice" role="status">
            {notice}
            <button aria-label="Dismiss notice" onClick={() => setNotice('')}>
              <X size={13} />
            </button>
          </div>
        )}
      </main>
      {open && (
        <aside className="wb-detail">
          <header>
            <span>
              {state.detail!.kind === 'node'
                ? 'NODE'
                : state.detail!.kind === 'task'
                  ? 'TASK INSTANCE'
                  : 'SESSION'}
            </span>
            <button
              aria-label="Close details"
              onClick={() => patch({ detail: null })}
            >
              <X size={18} />
            </button>
          </header>
          <div className="wb-detail-body">
            <div className="wb-detail-owner">{current.id}</div>
            {selectedTask ? (
              <>
                <h2>{selectedTask.title}</h2>
                <code className="wb-detail-id">
                  {selectedTask.definition} / {selectedTask.id}
                </code>
                <div className="wb-badges">
                  <Badge value={selectedTask.status} />
                  <Badge value={selectedTask.completion} />
                </div>
                <Resource value={selectedTask.resource} />
                <section className="wb-record">
                  <h3>
                    Completion conditions <code>done_when.all</code>
                  </h3>
                  {selectedTask.checks.map((c) => (
                    <div className="wb-check" key={c.field}>
                      {c.status === 'satisfied' ? (
                        <Check size={15} />
                      ) : (
                        <Clock size={15} />
                      )}
                      <div>
                        <code>{c.field}</code>
                        <span>{c.value}</span>
                        <small>Expected {c.expected}</small>
                      </div>
                    </div>
                  ))}
                </section>
                <RecordValues
                  title="State · self.state"
                  value={selectedTask.state}
                />
                <RecordValues
                  title="Observed · resource.state"
                  value={selectedTask.observed}
                />
                <small className="wb-muted">
                  {selectedTask.observedAt
                    ? 'Observed at ' + selectedTask.observedAt
                    : 'Not observed'}
                </small>
                <details className="wb-instructions">
                  <summary>Instructions</summary>
                  <p>{selectedTask.instruction}</p>
                </details>
              </>
            ) : selectedNode ? (
              <>
                <h2>{selectedNode.id}</h2>
                <code className="wb-detail-id">{selectedNode.uses}</code>
                <div className="wb-badges">
                  <Badge value={selectedNode.status} />
                  <span className="wb-muted">{selectedNode.scope} scope</span>
                </div>
                <div className="wb-record-time">Recorded {selectedNode.at}</div>
                {selectedNode.error && (
                  <div className="wb-error">
                    <AlertTriangle size={16} />
                    <p>{selectedNode.error}</p>
                  </div>
                )}
                <RecordValues
                  title="Inputs · recorded values"
                  value={selectedNode.inputs}
                />
                <RecordValues title="Outputs" value={selectedNode.outputs} />
                <RecordValues
                  title="Stored lifecycle"
                  value={{
                    scope: selectedNode.scope,
                    status: selectedNode.status,
                    [selectedNode.status === 'failed'
                      ? 'failed_at'
                      : selectedNode.status === 'cleaned'
                        ? 'cleaned_at'
                        : 'setup_at']: selectedNode.at,
                  }}
                />
                {selectedNode.id === 'initial_task' && (
                  <section className="wb-record">
                    <h3>Input binding · current configuration</h3>
                    <button
                      className="wb-binding"
                      onClick={() =>
                        patch({ detail: { kind: 'node', id: 'worker' } })
                      }
                    >
                      <code>nodes.worker.outputs.queue_dir</code>
                      <ArrowUpRight size={13} />
                    </button>
                  </section>
                )}
                <button
                  className="wb-text-link"
                  onClick={() => {
                    patch({ tab: 'conversation' });
                    setHighlight(
                      events.find(
                        (e) =>
                          e.session === active && e.node === selectedNode.id,
                      )?.id ?? '',
                    );
                  }}
                >
                  View session events
                  <ArrowUpRight size={14} />
                </button>
              </>
            ) : state.detail?.kind === 'session' ? (
              <>
                <h2>{current.id}</h2>
                <div className="wb-badges">
                  <Badge value={current.run} />
                  <Badge value={current.health} />
                </div>
                <Resource value={current.resource} />
                <RecordValues
                  title="Identity"
                  value={{
                    session: current.id,
                    workflow: current.workflow,
                    ...(current.parent
                      ? { parent_session: current.parent }
                      : {}),
                  }}
                />
                <section className="wb-record">
                  <h3>Status message</h3>
                  <p>{current.message || 'No status message'}</p>
                </section>
                <section className="wb-record">
                  <h3>
                    Tasks <span>{current.tasks.length}</span>
                  </h3>
                  {current.tasks.map((t) => (
                    <button
                      className="wb-detail-task"
                      key={t.id}
                      onClick={() =>
                        patch({ detail: { kind: 'task', id: t.id } })
                      }
                    >
                      {t.id}
                      <Badge value={t.completion} />
                      <ChevronRight size={13} />
                    </button>
                  ))}
                </section>
                <section className="wb-record">
                  <h3>Child sessions</h3>
                  {sessions
                    .filter((s) => s.parent === active)
                    .map((s) => (
                      <button
                        className="wb-text-link"
                        key={s.id}
                        onClick={() => select(s.id)}
                      >
                        {s.id}
                        <ArrowUpRight size={14} />
                      </button>
                    ))}
                  {!sessions.some((s) => s.parent === active) && (
                    <p>No child sessions</p>
                  )}
                </section>
              </>
            ) : (
              <div className="wb-empty-small">Record unavailable</div>
            )}
          </div>
        </aside>
      )}
      <Dialog open={create} onOpenChange={setCreate}>
        <DialogContent className="wb-dialog">
          <DialogHeader>
            <DialogTitle>New session</DialogTitle>
            <DialogDescription>
              Mock session · No process will be started
            </DialogDescription>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              start();
            }}
          >
            <label htmlFor="wb-name">Name</label>
            <Input
              id="wb-name"
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="session-name"
            />
            <label htmlFor="wb-resource">Resource</label>
            <Input
              id="wb-resource"
              required
              value={resource}
              onChange={(e) => setResource(e.target.value)}
              placeholder="https://… or resource identifier"
            />
            <div className="wb-create-workflow">
              <Workflow size={15} />
              <span>goal_reviewer</span>
              <small>Configured workflow</small>
            </div>
            <Button type="submit">
              Create local session
              <ArrowRight size={15} />
            </Button>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
