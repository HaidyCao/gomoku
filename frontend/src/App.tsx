import { useEffect, useMemo, useState } from 'react';
import type { CSSProperties } from 'react';
import {
  Bot,
  Check,
  Clipboard,
  Flag,
  History,
  LoaderCircle,
  Plus,
  RefreshCw,
  User,
} from 'lucide-react';

type Color = '' | 'black' | 'white';
type StoneColor = Exclude<Color, ''>;
type GameMode = 'human-agent' | 'agent-agent';
type Player = '' | 'human' | 'agent' | 'agent_black' | 'agent_white';
type AgentRole = 'agent' | 'agent_black' | 'agent_white';
type Status = 'playing' | 'draw' | 'black_won' | 'white_won';
type EndReason = '' | 'five_in_row' | 'draw' | 'resignation' | 'forbidden';
type AgentStrategy = 'think' | 'script';
type HistoryScope = 'mine' | 'all';
type WizardStep = 'mode' | 'color' | 'forbidden' | 'strategy' | 'confirm';

type Point = {
  row: number;
  col: number;
};

type ForbiddenPoint = {
  row: number;
  col: number;
  reason: string;
};

type Move = {
  moveNumber: number;
  row: number;
  col: number;
  color: StoneColor;
  player: Exclude<Player, ''>;
  createdAt: string;
};

type AgentState = {
  joined: boolean;
  thinking: boolean;
  joinedAt?: string;
  lastSeenAt?: string;
  thinkingSince?: string;
};

type GameState = {
  gameId: string;
  mode: GameMode;
  boardSize: number;
  humanColor: Color;
  agentColor: Color;
  forbidden: boolean;
  agentStrategy: AgentStrategy;
  agentState: AgentState;
  agentStates: Partial<Record<AgentRole, AgentState>>;
  humanToken?: string;
  agentToken?: string;
  agentBlackToken?: string;
  agentWhiteToken?: string;
  nextTurn?: Player;
  nextColor?: Color;
  status: Status;
  endReason?: EndReason;
  winner?: Color;
  winnerRole?: Player;
  resignedBy?: Player;
  winLine: Point[];
  forbiddenPoints?: ForbiddenPoint[];
  board: Color[][];
  moves: Move[];
  moveCount: number;
  createdAt: string;
  updatedAt: string;
};

type GameListItem = {
  gameId: string;
  mode: GameMode;
  humanColor: Color;
  agentColor: Color;
  forbidden: boolean;
  agentState: AgentState;
  agentStates: Partial<Record<AgentRole, AgentState>>;
  nextTurn?: Player;
  nextColor?: Color;
  status: Status;
  endReason?: EndReason;
  winner?: Color;
  winnerRole?: Player;
  resignedBy?: Player;
  moveCount: number;
  createdAt: string;
  updatedAt: string;
};

type StoredTokens = {
  humanToken?: string;
  agentToken?: string;
  agentBlackToken?: string;
  agentWhiteToken?: string;
};

type AgentPromptTarget = {
  role: AgentRole;
  label: string;
  token?: string;
};

type AgentEntry = {
  role: AgentRole;
  title: string;
  color: StoneColor;
  state: AgentState;
};

type NewGameOptions = {
  mode: GameMode;
  humanColor: StoneColor;
  forbidden: boolean;
  agentStrategy: AgentStrategy;
};

const tokenStorageKey = 'wuziqi.agentBattle.tokens.v1';
const ownerStorageKey = 'wuziqi.ownerId.v1';
const configuredApiBase = (import.meta.env.VITE_API_BASE || '').replace(/\/$/, '');
const apiBase = configuredApiBase;
const emptyAgentState: AgentState = {
  joined: false,
  thinking: false,
};

export function App() {
  const [game, setGame] = useState<GameState | null>(null);
  const [history, setHistory] = useState<GameListItem[]>([]);
  const [historyScope, setHistoryScope] = useState<HistoryScope>('mine');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [copiedPrompt, setCopiedPrompt] = useState<AgentRole | ''>('');
  const [showResignConfirm, setShowResignConfirm] = useState(false);
  const [pendingForbidden, setPendingForbidden] = useState<ForbiddenPoint | null>(null);
  const [wizardOpen, setWizardOpen] = useState(false);
  const [wizardStep, setWizardStep] = useState(0);
  const [draftMode, setDraftMode] = useState<GameMode>('human-agent');
  const [draftColor, setDraftColor] = useState<StoneColor>('black');
  const [draftForbidden, setDraftForbidden] = useState(false);
  const [draftStrategy, setDraftStrategy] = useState<AgentStrategy>('think');

  const ownerId = useMemo(() => readOwnerId(), []);
  const storedTokens = useMemo(() => readTokenStore(), [game?.gameId, history.length]);
  const forbiddenMap = useMemo(() => {
    const map = new Map<string, ForbiddenPoint>();
    for (const point of game?.forbiddenPoints || []) {
      map.set(pointKey(point.row, point.col), point);
    }
    return map;
  }, [game?.forbiddenPoints]);
  const activeTokens = game ? storedTokens[game.gameId] : undefined;
  const humanToken = game?.humanToken || activeTokens?.humanToken;
  const canHumanMove = Boolean(
    game && game.mode === 'human-agent' && game.status === 'playing' && game.nextTurn === 'human' && humanToken && !busy,
  );
  const canAttemptHumanResign = Boolean(game && game.mode === 'human-agent' && game.status === 'playing' && !busy);

  useEffect(() => {
    void bootstrap();
  }, []);

  useEffect(() => {
    if (!game || game.status !== 'playing') {
      return;
    }
    const timer = window.setInterval(() => {
      void refreshGame(game.gameId, true);
    }, 1400);
    return () => window.clearInterval(timer);
  }, [game?.gameId, game?.status]);

  useEffect(() => {
    void refreshHistory();
    // refreshHistory reads the latest historyScope/ownerId from its closure.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [historyScope]);

  async function bootstrap() {
    setLoading(true);
    try {
      const games = await listGames(historyScope === 'mine' ? ownerId : undefined);
      setHistory(games);
      if (games.length > 0) {
        await loadGame(games[0].gameId);
      } else {
        openWizard();
      }
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setLoading(false);
    }
  }

  async function refreshHistory() {
    const games = await listGames(historyScope === 'mine' ? ownerId : undefined);
    setHistory(games);
  }

  async function loadGame(gameId: string) {
    setBusy(true);
    try {
      const nextGame = withStoredTokens(await getGame(gameId));
      setGame(nextGame);
      setMessage('');
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setBusy(false);
    }
  }

  async function refreshGame(gameId: string, quiet = false) {
    try {
      const nextGame = withStoredTokens(await getGame(gameId));
      setGame(nextGame);
      await refreshHistory();
      if (!quiet) {
        setMessage('');
      }
    } catch (error) {
      if (!quiet) {
        setMessage(errorMessage(error));
      }
    }
  }

  async function createGame(options: NewGameOptions): Promise<boolean> {
    setBusy(true);
    try {
      const body: Record<string, unknown> = {
        mode: options.mode,
        forbidden: options.forbidden,
        agentStrategy: options.agentStrategy,
      };
      if (options.mode === 'human-agent') {
        body.humanColor = options.humanColor;
      }
      const nextGame = await api<GameState>('/api/games', {
        method: 'POST',
        headers: { 'X-Owner-Id': ownerId },
        body: JSON.stringify(body),
      });
      saveTokens(nextGame.gameId, {
        humanToken: nextGame.humanToken || '',
        agentToken: nextGame.agentToken || '',
        agentBlackToken: nextGame.agentBlackToken || '',
        agentWhiteToken: nextGame.agentWhiteToken || '',
      });
      setGame(nextGame);
      setCopiedPrompt('');
      setShowResignConfirm(false);
      setMessage('');
      await refreshHistory();
      return true;
    } catch (error) {
      setMessage(errorMessage(error));
      return false;
    } finally {
      setBusy(false);
    }
  }

  function openWizard() {
    setDraftMode('human-agent');
    setDraftColor('black');
    setDraftForbidden(false);
    setDraftStrategy('think');
    setWizardStep(0);
    setMessage('');
    setWizardOpen(true);
  }

  function closeWizard() {
    setWizardOpen(false);
    setWizardStep(0);
  }

  async function confirmWizard() {
    if (await createGame({
      mode: draftMode,
      humanColor: draftColor,
      forbidden: draftForbidden,
      agentStrategy: draftStrategy,
    })) {
      closeWizard();
    }
  }

  async function placeHumanMove(row: number, col: number) {
    if (!game || !canHumanMove || !humanToken || game.board[row]?.[col]) {
      return;
    }
    setBusy(true);
    try {
      const nextGame = await api<GameState>(`/api/games/${game.gameId}/moves`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${humanToken}`,
        },
        body: JSON.stringify({ row, col }),
      });
      setGame(withStoredTokens(nextGame));
      setMessage('');
      await refreshHistory();
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setBusy(false);
    }
  }

  function handleCellClick(row: number, col: number) {
    if (!game || !canHumanMove || game.board[row]?.[col]) {
      return;
    }
    const forbidden = forbiddenMap.get(pointKey(row, col));
    if (forbidden) {
      setPendingForbidden(forbidden);
      return;
    }
    void placeHumanMove(row, col);
  }

  async function confirmForbiddenMove() {
    if (!pendingForbidden) {
      return;
    }
    const { row, col } = pendingForbidden;
    setPendingForbidden(null);
    await placeHumanMove(row, col);
  }

  function requestHumanResign() {
    if (!game || !canAttemptHumanResign) {
      return;
    }
    if (!humanToken) {
      setMessage('当前浏览器没有这局的人类 token，不能代表我方认输');
      return;
    }
    setShowResignConfirm(true);
  }

  async function confirmHumanResign() {
    if (!game || !humanToken || !canAttemptHumanResign) {
      setShowResignConfirm(false);
      return;
    }
    setBusy(true);
    try {
      const nextGame = await api<GameState>(`/api/games/${game.gameId}/resign`, {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${humanToken}`,
        },
      });
      setGame(withStoredTokens(nextGame));
      setShowResignConfirm(false);
      setMessage('');
      await refreshHistory();
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setBusy(false);
    }
  }

  async function copyPrompt(role: AgentRole) {
    if (!game) {
      return;
    }
    const token = tokenForAgentRole(game, role);
    if (!token) {
      setMessage('当前浏览器没有这位 Agent 的 token');
      return;
    }
    try {
      await copyText(buildAgentPrompt(game, role, token));
      setCopiedPrompt(role);
      window.setTimeout(() => setCopiedPrompt(''), 1800);
      setMessage('');
    } catch (error) {
      setMessage(errorMessage(error));
    }
  }

  const lastMove = game?.moves.at(-1);
  const winSet = new Set((game?.winLine || []).map((point) => pointKey(point.row, point.col)));

  const wizardSteps: WizardStep[] =
    draftMode === 'agent-agent'
      ? ['mode', 'forbidden', 'strategy', 'confirm']
      : ['mode', 'color', 'forbidden', 'strategy', 'confirm'];
  const currentWizardStep = wizardSteps[Math.min(wizardStep, wizardSteps.length - 1)];
  const isLastWizardStep = wizardStep >= wizardSteps.length - 1;

  return (
    <div className="app">
      <header className="topbar">
        <div>
          <p className="eyebrow">Agent Gomoku</p>
          <h1>五子棋 Agent 对战</h1>
        </div>
        <button className="icon-button" onClick={() => void refreshGame(game!.gameId)} disabled={!game || busy}>
          <RefreshCw size={18} />
          <span>刷新</span>
        </button>
      </header>

      <main className="game-layout">
        <section className="board-panel" aria-label="棋盘">
          <div className="status-row">
            <StatusPill game={game} loading={loading} />
            {lastMove ? (
              <div className="last-move">
                最后一步 {colorLabel(lastMove.color)} {lastMove.row},{lastMove.col}
              </div>
            ) : (
              <div className="last-move">等待开局</div>
            )}
          </div>

          <div className="board-shell">
            {loading || !game ? (
              <div className="loading-board">
                <LoaderCircle className="spin" size={28} />
              </div>
            ) : (
              <div
                className={`board-grid ${canHumanMove ? 'playable' : ''}`}
                style={{ '--board-size': game.boardSize } as CSSProperties}
              >
                {game.board.flatMap((rowValues, row) =>
                  rowValues.map((cell, col) => {
                    const isLast = lastMove?.row === row && lastMove?.col === col;
                    const isWin = winSet.has(pointKey(row, col));
                    const isForbidden = !cell && forbiddenMap.has(pointKey(row, col));
                    return (
                      <button
                        key={`${row}-${col}`}
                        className={`cell ${isLast ? 'last' : ''} ${isWin ? 'winning' : ''} ${isForbidden ? 'forbidden' : ''}`}
                        aria-label={`${row},${col}${cell ? ` ${colorLabel(cell)}` : isForbidden ? ' 禁手点' : ''}`}
                        disabled={!canHumanMove || Boolean(cell)}
                        onClick={() => handleCellClick(row, col)}
                      >
                        {cell ? (
                          <span className={`stone ${cell}`} />
                        ) : isForbidden ? (
                          <span className="forbidden-mark" aria-hidden="true" />
                        ) : (
                          <span className="hover-stone" />
                        )}
                      </button>
                    );
                  }),
                )}
              </div>
            )}
          </div>
        </section>

        <aside className="side-panel">
          <section className="control-block">
            <div className="section-title">
              <User size={18} />
              <h2>新对局</h2>
            </div>
            <button className="primary-button" onClick={openWizard} disabled={busy}>
              <Plus size={18} />
              <span>新建对局</span>
            </button>
            {game?.mode === 'human-agent' ? (
              <button className="resign-button" onClick={requestHumanResign} disabled={!canAttemptHumanResign}>
                <Flag size={18} />
                <span>认输</span>
              </button>
            ) : null}
          </section>

          <section className="control-block">
            <div className="section-title">
              <Bot size={18} />
              <h2>Agent</h2>
            </div>
            {game ? (
              <>
                <div className="copy-stack">
                  {agentPromptTargets(game).map((target) => (
                    <button
                      key={target.role}
                      className="copy-button"
                      onClick={() => void copyPrompt(target.role)}
                      disabled={!target.token}
                    >
                      {copiedPrompt === target.role ? <Check size={18} /> : <Clipboard size={18} />}
                      <span>{copiedPrompt === target.role ? '已复制' : target.label}</span>
                    </button>
                  ))}
                </div>
                <div className="agent-card-list">
                  {agentEntries(game).map((entry) => (
                    <div className="agent-card" key={entry.role}>
                      <div className="agent-card-title">
                        <strong>{entry.title}</strong>
                        <span>{colorLabel(entry.color)}</span>
                      </div>
                      <div className={`agent-status ${agentStatusClass(entry.state)}`}>
                        <div>
                          <span>加入棋局</span>
                          <strong>{entry.state.joined ? '已加入' : '未加入'}</strong>
                        </div>
                        <div>
                          <span>思考状态</span>
                          <strong>{entry.state.thinking ? '正在思考' : '空闲'}</strong>
                        </div>
                        <div>
                          <span>最近心跳</span>
                          <strong>{agentLastSeenLabel(entry.state)}</strong>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
                <dl className="meta-grid">
                  {gameMetaItems(game).map((item) => (
                    <div key={item.label}>
                      <dt>{item.label}</dt>
                      <dd>{item.value}</dd>
                    </div>
                  ))}
                </dl>
              </>
            ) : null}
          </section>

          <section className="control-block">
            <div className="section-title">
              <History size={18} />
              <h2>历史棋局</h2>
            </div>
            <div className="segmented" aria-label="选择历史范围">
              <button
                className={historyScope === 'mine' ? 'active' : ''}
                onClick={() => setHistoryScope('mine')}
                disabled={busy}
              >
                我的对局
              </button>
              <button
                className={historyScope === 'all' ? 'active' : ''}
                onClick={() => setHistoryScope('all')}
                disabled={busy}
              >
                全部对局
              </button>
            </div>
            <div className="history-list">
              {history.length === 0 ? (
                <div className="history-empty">
                  {historyScope === 'mine' ? '还没有自己的对局，点上方“新建对局”开始。' : '还没有任何对局。'}
                </div>
              ) : (
                history.map((item) => (
                  <button
                    key={item.gameId}
                    className={`history-row ${item.gameId === game?.gameId ? 'selected' : ''}`}
                    onClick={() => void loadGame(item.gameId)}
                  >
                    <span>{shortId(item.gameId)}</span>
                    <span>{historyLabel(item)}</span>
                  </button>
                ))
              )}
            </div>
          </section>

          <section className="control-block moves-block">
            <div className="section-title">
              <Clipboard size={18} />
              <h2>落子</h2>
            </div>
            <ol className="move-list">
              {(game?.moves || []).slice(-12).reverse().map((move, index) => (
                <li key={move.moveNumber} className={index === 0 ? 'latest' : ''}>
                  <span className="move-no">{move.moveNumber}</span>
                  <span className={`move-stone ${move.color}`} role="img" aria-label={colorLabel(move.color)} />
                  <span className="move-player">{playerLabel(move.player, game || undefined)}</span>
                  <span className="move-coord">
                    {move.row},{move.col}
                  </span>
                </li>
              ))}
              {game && game.moves.length === 0 ? <li className="move-empty">还没有落子</li> : null}
            </ol>
          </section>

          {message ? <div className="message">{message}</div> : null}
        </aside>
      </main>

      {wizardOpen ? (
        <div className="modal-backdrop" role="presentation">
          <div className="confirm-dialog wizard-dialog" role="dialog" aria-modal="true" aria-labelledby="wizard-title">
            <h2 id="wizard-title">新建对局</h2>
            <p className="wizard-steps">
              第 {Math.min(wizardStep, wizardSteps.length - 1) + 1} / {wizardSteps.length} 步
            </p>
            <div className="wizard-body">
              {currentWizardStep === 'mode' ? (
                <div className="wizard-field">
                  <span className="wizard-label">对战模式</span>
                  <div className="segmented">
                    <button className={draftMode === 'human-agent' ? 'active' : ''} onClick={() => setDraftMode('human-agent')}>
                      人机
                    </button>
                    <button className={draftMode === 'agent-agent' ? 'active' : ''} onClick={() => setDraftMode('agent-agent')}>
                      机机
                    </button>
                  </div>
                  <p className="wizard-hint">
                    {draftMode === 'human-agent' ? '你与一个 Agent 对弈。' : '两个 Agent 各执黑白互相对弈，你旁观。'}
                  </p>
                </div>
              ) : null}
              {currentWizardStep === 'color' ? (
                <div className="wizard-field">
                  <span className="wizard-label">我方执子</span>
                  <div className="segmented">
                    <button className={draftColor === 'black' ? 'active' : ''} onClick={() => setDraftColor('black')}>
                      执黑先手
                    </button>
                    <button className={draftColor === 'white' ? 'active' : ''} onClick={() => setDraftColor('white')}>
                      执白后手
                    </button>
                  </div>
                </div>
              ) : null}
              {currentWizardStep === 'forbidden' ? (
                <div className="wizard-field">
                  <span className="wizard-label">禁手规则</span>
                  <div className="segmented">
                    <button className={!draftForbidden ? 'active' : ''} onClick={() => setDraftForbidden(false)}>
                      无禁手
                    </button>
                    <button className={draftForbidden ? 'active' : ''} onClick={() => setDraftForbidden(true)}>
                      开启禁手
                    </button>
                  </div>
                  <p className="wizard-hint">开启后，黑棋走出三三 / 四四 / 长连即判负，白棋不受限。</p>
                </div>
              ) : null}
              {currentWizardStep === 'strategy' ? (
                <div className="wizard-field">
                  <span className="wizard-label">Agent 对战方式</span>
                  <div className="segmented">
                    <button className={draftStrategy === 'think' ? 'active' : ''} onClick={() => setDraftStrategy('think')}>
                      逐步思考
                    </button>
                    <button className={draftStrategy === 'script' ? 'active' : ''} onClick={() => setDraftStrategy('script')}>
                      生成脚本
                    </button>
                  </div>
                  <p className="wizard-hint">
                    {draftStrategy === 'think'
                      ? '复制提示词给外部 LLM，它每一步实时读盘分析后落子。'
                      : '提示词改为让外部 LLM 先写一个自带 AI 的脚本，由脚本自动循环调用接口对战。'}
                  </p>
                </div>
              ) : null}
              {currentWizardStep === 'confirm' ? (
                <dl className="wizard-summary">
                  <div>
                    <dt>对战模式</dt>
                    <dd>{draftMode === 'agent-agent' ? '机机' : '人机'}</dd>
                  </div>
                  {draftMode === 'human-agent' ? (
                    <div>
                      <dt>我方执子</dt>
                      <dd>{draftColor === 'black' ? '执黑先手' : '执白后手'}</dd>
                    </div>
                  ) : null}
                  <div>
                    <dt>禁手规则</dt>
                    <dd>{draftForbidden ? '开启' : '无'}</dd>
                  </div>
                  <div>
                    <dt>Agent 方式</dt>
                    <dd>{draftStrategy === 'think' ? '逐步思考' : '生成脚本'}</dd>
                  </div>
                </dl>
              ) : null}
            </div>
            <div className="dialog-actions">
              {wizardStep > 0 ? (
                <button className="secondary-button" onClick={() => setWizardStep((step) => Math.max(0, step - 1))} disabled={busy}>
                  上一步
                </button>
              ) : (
                <button className="secondary-button" onClick={closeWizard} disabled={busy}>
                  取消
                </button>
              )}
              {isLastWizardStep ? (
                <button className="primary-button" onClick={() => void confirmWizard()} disabled={busy}>
                  <Plus size={18} />
                  <span>创建并开始</span>
                </button>
              ) : (
                <button className="primary-button" onClick={() => setWizardStep((step) => step + 1)} disabled={busy}>
                  <span>下一步</span>
                </button>
              )}
            </div>
          </div>
        </div>
      ) : null}

      {pendingForbidden ? (
        <div className="modal-backdrop" role="presentation">
          <div className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="forbidden-title">
            <h2 id="forbidden-title">禁手预警</h2>
            <p>
              ({pendingForbidden.row}, {pendingForbidden.col}) 是{forbiddenReasonLabel(pendingForbidden.reason)}禁手点，落子将立即判负。确定要走这里吗？
            </p>
            <div className="dialog-actions">
              <button className="secondary-button" onClick={() => setPendingForbidden(null)} disabled={busy}>
                取消
              </button>
              <button className="danger-button" onClick={() => void confirmForbiddenMove()} disabled={busy}>
                <Flag size={18} />
                <span>仍然落子</span>
              </button>
            </div>
          </div>
        </div>
      ) : null}

      {showResignConfirm ? (
        <div className="modal-backdrop" role="presentation">
          <div className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="resign-title">
            <h2 id="resign-title">确认认输</h2>
            <p>棋局会立即结束，对方获胜。</p>
            <div className="dialog-actions">
              <button className="secondary-button" onClick={() => setShowResignConfirm(false)} disabled={busy}>
                取消
              </button>
              <button className="danger-button" onClick={() => void confirmHumanResign()} disabled={busy}>
                <Flag size={18} />
                <span>确认认输</span>
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function StatusPill({ game, loading }: { game: GameState | null; loading: boolean }) {
  if (loading || !game) {
    return <div className="status-pill neutral">加载中</div>;
  }
  if (game.status === 'draw') {
    return <div className="status-pill neutral">平局</div>;
  }
  if (game.winner) {
    if (game.endReason === 'forbidden') {
      return <div className="status-pill ended">黑棋禁手判负</div>;
    }
    if (game.endReason === 'resignation') {
      return <div className="status-pill ended">{playerLabel(game.resignedBy, game)}认输</div>;
    }
    return <div className="status-pill ended">{colorLabel(game.winner)}获胜</div>;
  }
  if (game.nextTurn === 'human') {
    return <div className="status-pill human">轮到我方 {colorLabel(game.nextColor || '')}</div>;
  }
  if (isAgentRole(game.nextTurn)) {
    const state = agentStateForRole(game, game.nextTurn);
    const label = playerLabel(game.nextTurn, game);
    if (!state.joined) {
      return <div className="status-pill agent">等待 {label} 加入</div>;
    }
    if (state.thinking) {
      return <div className="status-pill thinking">{label} 正在思考</div>;
    }
    return <div className="status-pill agent">等待 {label} 落子 {colorLabel(game.nextColor || '')}</div>;
  }
  return <div className="status-pill neutral">等待下一手</div>;
}

async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }

  const response = await fetch(`${apiBase}${path}`, {
    ...init,
    headers,
  });

  if (!response.ok) {
    let message = `请求失败：${response.status}`;
    try {
      const payload = (await response.json()) as { error?: string };
      if (payload.error) {
        message = payload.error;
      }
    } catch {
      // Keep the status message when the response is not JSON.
    }
    throw new Error(message);
  }
  return (await response.json()) as T;
}

function listGames(owner?: string) {
  const ownerParam = owner ? `&owner=${encodeURIComponent(owner)}` : '';
  return api<GameListItem[]>(`/api/games?limit=20${ownerParam}`);
}

function getGame(gameId: string) {
  return api<GameState>(`/api/games/${gameId}`);
}

function readOwnerId(): string {
  try {
    const existing = window.localStorage.getItem(ownerStorageKey);
    if (existing) {
      return existing;
    }
    const generated =
      typeof crypto !== 'undefined' && 'randomUUID' in crypto
        ? crypto.randomUUID()
        : `owner-${Math.random().toString(36).slice(2)}${Date.now().toString(36)}`;
    window.localStorage.setItem(ownerStorageKey, generated);
    return generated;
  } catch {
    return '';
  }
}

function readTokenStore(): Record<string, StoredTokens> {
  try {
    const raw = window.localStorage.getItem(tokenStorageKey);
    if (!raw) {
      return {};
    }
    return JSON.parse(raw) as Record<string, StoredTokens>;
  } catch {
    return {};
  }
}

function saveTokens(gameId: string, tokens: StoredTokens) {
  const store = readTokenStore();
  store[gameId] = tokens;
  window.localStorage.setItem(tokenStorageKey, JSON.stringify(store));
}

function withStoredTokens(game: GameState): GameState {
  const tokens = readTokenStore()[game.gameId];
  if (!tokens) {
    return game;
  }
  return {
    ...game,
    humanToken: game.humanToken || tokens.humanToken,
    agentToken: game.agentToken || tokens.agentToken,
    agentBlackToken: game.agentBlackToken || tokens.agentBlackToken,
    agentWhiteToken: game.agentWhiteToken || tokens.agentWhiteToken,
  };
}

function forbiddenRuleText(game: GameState) {
  return game.forbidden
    ? '本局开启禁手（仅限黑棋）：黑棋走出长连（6 子及以上）、四四或三三即判负；黑棋先连成恰好 5 子则获胜。白棋不受任何限制。'
    : '本局没有禁手规则，任意一方先连成 5 子或更多即获胜。';
}

// forbiddenAgentNote tells a black agent how to read the server-published禁手点
// set so it can avoid losing. It is empty unless this agent plays black under 禁手.
function forbiddenAgentNote(game: GameState, color: StoneColor) {
  if (!game.forbidden || color !== 'black') {
    return '';
  }
  return '\n- 你执黑：轮到你时，返回 JSON 的 "forbiddenPoints" 字段（[{row,col,reason}]）会列出当前所有禁手点；落子前必须从候选点中排除它们，落在禁手点会立即判负。';
}

function buildAgentPrompt(game: GameState, role: AgentRole, token: string) {
  const baseUrl = configuredApiBase || window.location.origin;
  const color = agentColorForRole(game, role);
  const opponent = game.mode === 'agent-agent' ? oppositeAgentLabel(role) : `人类（${colorLabel(game.humanColor)}）`;
  const waitTarget = game.mode === 'agent-agent' ? '另一位 Agent' : '人类';
  if (game.agentStrategy === 'script') {
    return buildScriptAgentPrompt(game, role, token, baseUrl, color, opponent);
  }
  return buildThinkAgentPrompt(game, role, token, baseUrl, color, opponent, waitTarget);
}

function buildThinkAgentPrompt(
  game: GameState,
  role: AgentRole,
  token: string,
  baseUrl: string,
  color: StoneColor,
  opponent: string,
  waitTarget: string,
) {
  const expectedTurn = role;
  return `你是一个通过 HTTP 接口参加五子棋对战的 Agent。

棋局信息：
- baseUrl: ${baseUrl}
- gameId: ${game.gameId}
- agentToken: ${token}
- 你的身份: ${role}
- 你的棋色: ${color}
- 对手: ${opponent}
- 对战模式: ${game.mode}

规则：
- 棋盘是 15x15。
- API 坐标是 0-based，row 和 col 都是 0 到 14。
- 黑棋先手。
- 横向、纵向、任一斜向连续 5 枚或更多同色棋子即获胜。
- ${forbiddenRuleText(game)}${forbiddenAgentNote(game, color)}

参加方式：
1. 先加入棋局，让页面知道你已经接管 Agent：
   curl -s -X POST "${baseUrl}/api/games/${game.gameId}/agent/join" \\
     -H "Authorization: Bearer ${token}"
2. 读取棋局：
   curl -s "${baseUrl}/api/games/${game.gameId}"
3. 只有当返回 JSON 里的 "status" 是 "playing"、"nextTurn" 是 "${expectedTurn}"，且 "nextColor" 是 "${color}" 时，你才能落子。
4. 当轮到你时，先标记正在思考：
   curl -s -X POST "${baseUrl}/api/games/${game.gameId}/agent/status" \\
     -H "Authorization: Bearer ${token}" \\
     -H "Content-Type: application/json" \\
     -d '{"thinking":true}'
5. 从 "board" 字段分析棋盘。空位是 ""，黑棋是 "black"，白棋是 "white"。
6. 落子：
   curl -s -X POST "${baseUrl}/api/games/${game.gameId}/moves" \\
     -H "Authorization: Bearer ${token}" \\
     -H "Content-Type: application/json" \\
     -d '{"row":7,"col":7}'
7. 如果你判断已经没有合理胜算，可以认输。认输会立即结束棋局：
   curl -s -X POST "${baseUrl}/api/games/${game.gameId}/resign" \\
     -H "Authorization: Bearer ${token}"
8. 如果你没有落子、已经落子、已经认输、或者正在等待${waitTarget}，请标记不在思考：
   curl -s -X POST "${baseUrl}/api/games/${game.gameId}/agent/status" \\
     -H "Authorization: Bearer ${token}" \\
     -H "Content-Type: application/json" \\
     -d '{"thinking":false}'
9. 继续轮询第 2 步，等待${waitTarget}回合结束。

请你自主选择最优落子。不要调用任何不存在的接口，不要在 nextTurn 不是 "${expectedTurn}" 或 nextColor 不是 "${color}" 时落子。`;
}

function buildScriptAgentPrompt(
  game: GameState,
  role: AgentRole,
  token: string,
  baseUrl: string,
  color: StoneColor,
  opponent: string,
) {
  const forbiddenLine =
    game.forbidden && color === 'black'
      ? `- 你执黑且本局开启禁手：用返回的 forbiddenPoints 字段排除所有禁手点（落在禁手点立即判负），不必自己重算禁手。在挑选落点前先这样过滤候选：
    // JavaScript
    const banned = new Set((state.forbiddenPoints || []).map(p => p.row + ',' + p.col));
    candidates = candidates.filter(m => !banned.has(m.row + ',' + m.col));
    # Python
    banned = {(p["row"], p["col"]) for p in state.get("forbiddenPoints", [])}
    candidates = [m for m in candidates if (m["row"], m["col"]) not in banned]`
      : '';
  return `你是一名工程师，请为下面这局五子棋编写一个“可独立运行的脚本”来代替你自动落子。直接产出完整可运行的代码（Node.js 18+ 用内置 fetch，或 Python 3 用 requests），脚本启动后自己跑完整局对战，你不需要每一步再介入。

棋局信息：
- baseUrl: ${baseUrl}
- gameId: ${game.gameId}
- agentToken: ${token}
- 你的身份(nextTurn 取值): ${role}
- 你的棋色: ${color}
- 对手: ${opponent}
- 对战模式: ${game.mode}

规则：
- 棋盘 15x15，坐标 0-based（row、col 均 0..14），黑棋先手。
- 横、纵、任一斜向连续 5 枚或更多同色即获胜。
- ${forbiddenRuleText(game)}

HTTP 接口（路径相对 baseUrl，统一带鉴权头 Authorization: Bearer ${token}）：
- POST /api/games/${game.gameId}/agent/join                                  启动时调用一次，加入棋局
- GET  /api/games/${game.gameId}                                             读取棋局，返回 JSON
- POST /api/games/${game.gameId}/agent/status  body {"thinking":true|false}  标记是否在思考
- POST /api/games/${game.gameId}/moves         body {"row":R,"col":C}         落子
- POST /api/games/${game.gameId}/resign                                      认输（立即结束）
返回 JSON 关键字段：status("playing"|"draw"|"black_won"|"white_won")、nextTurn、nextColor("black"|"white")、board（15x15 数组，空位 ""、黑 "black"、白 "white"）；当你执黑且开启禁手时还有 forbiddenPoints（[{row,col,reason}]，列出当前所有禁手点）。

脚本应实现的循环：
1. 启动时 POST /agent/join。
2. 轮询 GET 棋局；若 status 不是 "playing" 则结束退出。
3. 仅当 nextTurn == "${role}" 且 nextColor == "${color}" 时才轮到你：先 POST /agent/status {"thinking":true}，用本地算法在 board 上算出落点，POST /moves 落子，再 POST /agent/status {"thinking":false}。
4. 否则等待约 1 秒后回到第 2 步。
5. 对 409/401 等错误要容错：重新读取棋局后再试，不要直接崩溃。

本地落子算法（请实现一个合理的启发式，不要随机乱下）：
- 自己能立即成五就成五；对手出现“四”（差一子成五）必须封堵。
- 否则给每个空点打分：扩展自己的连子/活三/活四加分，封堵对手威胁加分，靠近棋盘中心略加分，取最高分的点。
${forbiddenLine ? forbiddenLine + '\n' : ''}请只输出完整脚本代码与简短运行说明（如何填入 baseUrl/gameId/token 及启动命令），不要省略关键逻辑，不要调用不存在的接口。`;
}

async function copyText(text: string) {
  let clipboardError: unknown;
  if (navigator.clipboard?.writeText && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch (error) {
      clipboardError = error;
    }
  }

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.style.position = 'fixed';
  textarea.style.top = '0';
  textarea.style.left = '0';
  textarea.style.width = '1px';
  textarea.style.height = '1px';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, textarea.value.length);
  const copied = document.execCommand('copy');
  document.body.removeChild(textarea);
  if (!copied) {
    throw clipboardError instanceof Error ? clipboardError : new Error('复制失败，请确认浏览器允许剪贴板访问');
  }
}

function strategyLabel(strategy: AgentStrategy) {
  return strategy === 'script' ? '生成脚本' : '逐步思考';
}

function forbiddenReasonLabel(reason: string) {
  switch (reason) {
    case 'overline':
      return '长连';
    case 'double_four':
      return '四四';
    case 'double_three':
      return '三三';
    default:
      return '';
  }
}

function colorLabel(color: Color) {
  if (color === 'black') {
    return '黑棋';
  }
  if (color === 'white') {
    return '白棋';
  }
  return '';
}

function playerLabel(player?: Player, game?: GameState | GameListItem) {
  if (player === 'human') {
    return '我方';
  }
  if (player === 'agent') {
    return 'Agent';
  }
  if (player === 'agent_black') {
    return game?.mode === 'agent-agent' ? '黑棋 Agent' : 'Agent';
  }
  if (player === 'agent_white') {
    return game?.mode === 'agent-agent' ? '白棋 Agent' : 'Agent';
  }
  return '一方';
}

function historyLabel(item: GameListItem) {
  if (item.status === 'draw') {
    return `平局 ${item.moveCount} 手`;
  }
  if (item.winner) {
    if (item.endReason === 'forbidden') {
      return `黑棋禁手 ${item.moveCount} 手`;
    }
    if (item.endReason === 'resignation') {
      return `${playerLabel(item.resignedBy, item)}认输 ${item.moveCount} 手`;
    }
    return `${colorLabel(item.winner)}胜 ${item.moveCount} 手`;
  }
  if (isAgentRole(item.nextTurn)) {
    const state = agentStateForRole(item, item.nextTurn);
    const label = playerLabel(item.nextTurn, item);
    if (!state.joined) {
      return `${label} 未加入 ${item.moveCount} 手`;
    }
    if (state.thinking) {
      return `${label} 思考中 ${item.moveCount} 手`;
    }
    return `${label} ${item.moveCount} 手`;
  }
  return `${item.nextTurn === 'human' ? '我方' : '等待'} ${item.moveCount} 手`;
}

function agentPromptTargets(game: GameState): AgentPromptTarget[] {
  if (game.mode === 'agent-agent') {
    return [
      {
        role: 'agent_black',
        label: '复制黑棋 Agent 提示词',
        token: tokenForAgentRole(game, 'agent_black'),
      },
      {
        role: 'agent_white',
        label: '复制白棋 Agent 提示词',
        token: tokenForAgentRole(game, 'agent_white'),
      },
    ];
  }
  return [
    {
      role: 'agent',
      label: '复制提示词',
      token: tokenForAgentRole(game, 'agent'),
    },
  ];
}

function agentEntries(game: GameState): AgentEntry[] {
  if (game.mode === 'agent-agent') {
    return [
      {
        role: 'agent_black',
        title: '黑棋 Agent',
        color: 'black',
        state: agentStateForRole(game, 'agent_black'),
      },
      {
        role: 'agent_white',
        title: '白棋 Agent',
        color: 'white',
        state: agentStateForRole(game, 'agent_white'),
      },
    ];
  }
  return [
    {
      role: 'agent',
      title: 'Agent',
      color: agentColorForRole(game, 'agent'),
      state: agentStateForRole(game, 'agent'),
    },
  ];
}

function gameMetaItems(game: GameState) {
  if (game.mode === 'agent-agent') {
    return [
      { label: '棋局', value: shortId(game.gameId) },
      { label: '模式', value: '机机对战' },
      { label: '黑棋', value: '黑棋 Agent' },
      { label: '白棋', value: '白棋 Agent' },
      { label: '禁手', value: game.forbidden ? '开启' : '无' },
      { label: 'Agent 方式', value: strategyLabel(game.agentStrategy) },
      { label: '手数', value: String(game.moveCount) },
    ];
  }
  return [
    { label: '棋局', value: shortId(game.gameId) },
    { label: '模式', value: '人机对战' },
    { label: '我方', value: colorLabel(game.humanColor) },
    { label: 'Agent', value: colorLabel(game.agentColor) },
    { label: '禁手', value: game.forbidden ? '开启' : '无' },
    { label: 'Agent 方式', value: strategyLabel(game.agentStrategy) },
    { label: '手数', value: String(game.moveCount) },
  ];
}

function tokenForAgentRole(game: GameState, role: AgentRole) {
  if (role === 'agent_black') {
    return game.agentBlackToken;
  }
  if (role === 'agent_white') {
    return game.agentWhiteToken;
  }
  return game.agentToken;
}

function agentColorForRole(game: GameState, role: AgentRole): StoneColor {
  if (role === 'agent_black') {
    return 'black';
  }
  if (role === 'agent_white') {
    return 'white';
  }
  return game.agentColor === 'white' ? 'white' : 'black';
}

function agentStateForRole(game: Pick<GameState | GameListItem, 'mode' | 'agentState' | 'agentStates'>, role: AgentRole): AgentState {
  return game.agentStates?.[role] || (role === 'agent' ? game.agentState : emptyAgentState);
}

function isAgentRole(player?: Player): player is AgentRole {
  return player === 'agent' || player === 'agent_black' || player === 'agent_white';
}

function oppositeAgentLabel(role: AgentRole) {
  if (role === 'agent_black') {
    return '白棋 Agent';
  }
  if (role === 'agent_white') {
    return '黑棋 Agent';
  }
  return '人类';
}

function shortId(id: string) {
  return id.length > 10 ? `${id.slice(0, 6)}...${id.slice(-4)}` : id;
}

function pointKey(row: number, col: number) {
  return `${row}:${col}`;
}

function errorMessage(error: unknown) {
  if (error instanceof Error) {
    return error.message;
  }
  return '操作失败';
}

function agentStatusClass(agentState: AgentState) {
  if (agentState.thinking) {
    return 'thinking';
  }
  if (agentState.joined) {
    return 'joined';
  }
  return 'waiting';
}

function agentLastSeenLabel(agentState: AgentState) {
  if (agentState.thinkingSince) {
    return timeLabel(agentState.thinkingSince);
  }
  if (agentState.lastSeenAt) {
    return timeLabel(agentState.lastSeenAt);
  }
  return '暂无';
}

function timeLabel(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return '暂无';
  }
  return date.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}
