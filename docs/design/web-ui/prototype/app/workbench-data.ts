export type Run = 'up' | 'down';
export type Lifecycle = 'produced' | 'failed' | 'cleaned';
export type Completion = 'satisfied' | 'unsatisfied' | 'pending';
export type NodeRecord = {
  id: string;
  uses: string;
  status: Lifecycle;
  scope: 'run' | 'session';
  inputs: Record<string, unknown>;
  outputs: Record<string, unknown>;
  error?: string;
  at: string;
};
export type TaskRecord = {
  id: string;
  definition: string;
  title: string;
  resource: string;
  status: Lifecycle;
  completion: Completion;
  instruction: string;
  state: Record<string, unknown>;
  observed: Record<string, unknown>;
  observedAt: string;
  checks: {
    field: string;
    value: string;
    expected: string;
    status: Completion;
  }[];
};
export type SessionRecord = {
  id: string;
  parent?: string;
  resource: string;
  run: Run;
  health: 'healthy' | 'unhealthy' | 'undeclared';
  message: string;
  workflow: string;
  tasks: TaskRecord[];
  nodes: NodeRecord[];
  capture?: string;
};
export type EventRecord = {
  id: string;
  session: string;
  origin?: string;
  time: string;
  type: string;
  source: string;
  body: string;
  task?: string;
  node?: string;
  related?: string;
};
const resource = 'https://github.com/kecbigmt/plecture';
function task(id: string, title: string, completion: Completion): TaskRecord {
  return {
    id,
    definition: 'goal_review',
    title,
    resource: resource + '/blob/main/docs/language/tasks.md',
    status: 'produced',
    completion,
    instruction: title + '. Record the observations and review verdict.',
    state: completion === 'satisfied' ? { verdict: 'approved' } : {},
    observed: completion === 'pending' ? {} : { checklist_status: 'SUCCESS' },
    observedAt: completion === 'pending' ? '' : '14:12:04',
    checks: [
      {
        field: 'resource.state.checklist_status',
        value: completion === 'pending' ? 'Not observed' : 'SUCCESS',
        expected: 'SUCCESS',
        status: completion === 'pending' ? 'pending' : 'satisfied',
      },
      {
        field: 'self.state.verdict',
        value: completion === 'satisfied' ? 'approved' : 'Not recorded',
        expected: 'approved',
        status: completion === 'satisfied' ? 'satisfied' : 'pending',
      },
    ],
  };
}
function nodes(name: string, failed = false): NodeRecord[] {
  return [
    {
      id: 'pane',
      uses: 'pane',
      scope: 'run',
      status: 'produced',
      inputs: { session_name: name },
      outputs: { session_name: name },
      at: '14:00:01',
    },
    {
      id: 'worker',
      uses: 'official.codex.exec_runtime',
      scope: 'run',
      status: failed ? 'failed' : 'produced',
      inputs: { workspace_dir: '/demo/' + name },
      outputs: failed ? {} : { queue_dir: '/demo/runtime/' + name + '/queue' },
      error: failed
        ? 'Runtime startup failed: executable not found: codex'
        : undefined,
      at: '14:00:02',
    },
    {
      id: 'initial_task',
      uses: 'initial_task',
      scope: 'run',
      status: failed ? 'cleaned' : 'produced',
      inputs: failed
        ? {}
        : {
            queue_dir: '/demo/runtime/' + name + '/queue',
            task: 'goal_review',
          },
      outputs: failed ? {} : {},
      at: failed ? '13:58:00' : '14:00:03',
    },
  ];
}
function session(id: string, title: string, parent?: string): SessionRecord {
  return {
    id,
    parent,
    resource,
    run: 'up',
    health: 'healthy',
    message: 'Review in progress',
    workflow: 'goal_reviewer',
    tasks: [task('review', title, 'pending')],
    nodes: nodes(id),
    capture: `$ plect attach ${id}\n\nWorkspace: /demo/${id}\n\n› ${title}\n\n• Inspecting the changes.\n• Collecting verification results.\n\n`,
  };
}
export const seedSessions: SessionRecord[] = [
  session('release', 'Review the next release'),
  {
    ...session('config', 'Review configuration loader changes', 'release'),
    message: 'Compatibility checked. Reviewing reference resolution.',
    tasks: [
      task('compatibility', 'Check compatibility with existing configuration', 'satisfied'),
      task('references', 'Check definition reference resolution', 'pending'),
    ],
  },
  {
    ...session('compat', 'Existing configuration compatibility', 'config'),
    run: 'down',
    health: 'undeclared',
    message: 'Review verdict recorded',
    tasks: [task('review', 'Check compatibility with existing configuration', 'satisfied')],
    nodes: nodes('compat').map((n) => ({ ...n, status: 'cleaned' })),
  },
  session('onboarding', 'Review onboarding instructions', 'release'),
  {
    ...session('runtime', 'Check runtime startup', 'release'),
    run: 'down',
    health: 'unhealthy',
    message: 'Could not start codex',
    nodes: nodes('runtime', true).map((n) =>
      n.id === 'pane'
        ? { ...n, status: 'cleaned', at: '14:14:03' }
        : n.id === 'worker'
          ? { ...n, at: '14:14:02' }
          : n,
    ),
    tasks: [],
    capture: undefined,
  },
  session('docs', 'Update the CLI reference'),
  session('examples', 'Check configuration examples', 'docs'),
];
export const seedEvents: EventRecord[] = [
  {
    id: 'e01',
    session: 'release',
    time: '13:50:00',
    type: 'user.emit',
    source: 'web',
    body: 'Review the configuration loader and onboarding instructions before release.',
  },
  {
    id: 'e02',
    session: 'config',
    time: '14:00:00',
    type: 'lifecycle.created',
    source: 'plect',
    body: 'Session created',
  },
  {
    id: 'e03',
    session: 'config',
    time: '14:00:03',
    type: 'plect.instruction',
    source: 'plect',
    body: 'Review the configuration loader changes. Check existing configuration compatibility and definition reference resolution.',
    task: 'references',
  },
  {
    id: 'e04',
    session: 'config',
    time: '14:02:10',
    type: 'user.emit',
    source: 'web',
    body: 'Prioritize checking whether existing configuration files still work.',
  },
  {
    id: 'e05',
    session: 'compat',
    time: '14:05:00',
    type: 'lifecycle.created',
    source: 'plect',
    body: 'Session created',
  },
  {
    id: 'e06',
    session: 'config',
    time: '14:09:12',
    type: 'plect.status_message',
    source: 'plect',
    body: 'Existing configuration loads successfully. Checking reference resolution cases.',
  },
  {
    id: 'e07',
    session: 'config',
    origin: 'compat',
    time: '14:12:04',
    type: 'plect.terminal.done',
    source: 'plect',
    body: 'done_when satisfied for review',
    related: 'compat',
  },
  {
    id: 'e08',
    session: 'config',
    time: '14:12:20',
    type: 'user.emit',
    source: 'web',
    body: 'Thanks for checking compatibility. Please also check how missing references are handled.',
  },
  {
    id: 'e09',
    session: 'runtime',
    time: '14:14:02',
    type: 'plect.status_message',
    source: 'plect',
    body: 'Could not start codex. Check PATH in the runtime environment.',
    node: 'worker',
  },
  {
    id: 'e10',
    session: 'release',
    origin: 'runtime',
    time: '14:14:03',
    type: 'plect.terminal.escalate',
    source: 'plect',
    body: 'Runtime startup failed: executable not found: codex',
    related: 'runtime',
  },
  {
    id: 'e11',
    session: 'onboarding',
    time: '14:15:20',
    type: 'user.emit',
    source: 'web',
    body: 'Cover local environment setup. Production access permissions are outside this review.',
  },
  {
    id: 'e12',
    session: 'config',
    time: '14:16:08',
    type: 'plect.status_message',
    source: 'plect',
    body: 'Checking diagnostics for missing references.',
  },
  {
    id: 'e13',
    session: 'docs',
    time: '14:17:00',
    type: 'plect.instruction',
    source: 'plect',
    body: 'Compare CLI help output with the reference documentation.',
    task: 'review',
  },
];
export function ancestors(sessions: SessionRecord[], id: string): string[] {
  const result: string[] = [];
  const seen = new Set<string>();
  let s = sessions.find((x) => x.id === id);
  while (s?.parent && !seen.has(s.parent)) {
    seen.add(s.parent);
    result.unshift(s.parent);
    s = sessions.find((x) => x.id === s!.parent);
  }
  return result;
}
export function eventTarget(e: EventRecord) {
  return e.origin ?? e.session;
}
export function escalation(e: EventRecord) {
  return ['plect.tick.escalated', 'plect.terminal.escalate'].includes(e.type);
}
export function freshSession(id: string, resource: string): SessionRecord {
  return {
    id,
    resource,
    run: 'down',
    health: 'undeclared',
    message: '',
    workflow: 'goal_reviewer',
    tasks: [],
    nodes: [],
  };
}
