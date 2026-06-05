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
type EndReason = '' | 'five_in_row' | 'draw' | 'resignation';

type Point = {
  row: number;
  col: number;
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

const tokenStorageKey = 'wuziqi.agentBattle.tokens.v1';
const configuredApiBase = (import.meta.env.VITE_API_BASE || '').replace(/\/$/, '');
const apiBase = configuredApiBase;
const emptyAgentState: AgentState = {
  joined: false,
  thinking: false,
};

export function App() {
  const [game, setGame] = useState<GameState | null>(null);
  const [history, setHistory] = useState<GameListItem[]>([]);
  const [gameMode, setGameMode] = useState<GameMode>('human-agent');
  const [humanColor, setHumanColor] = useState<StoneColor>('black');
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [copiedPrompt, setCopiedPrompt] = useState<AgentRole | ''>('');
  const [showResignConfirm, setShowResignConfirm] = useState(false);

  const storedTokens = useMemo(() => readTokenStore(), [game?.gameId, history.length]);
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

  async function bootstrap() {
    setLoading(true);
    try {
      const games = await listGames();
      setHistory(games);
      if (games.length > 0) {
        await loadGame(games[0].gameId);
      } else {
        await createGame('human-agent', 'black');
      }
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setLoading(false);
    }
  }

  async function refreshHistory() {
    const games = await listGames();
    setHistory(games);
  }

  async function loadGame(gameId: string) {
    setBusy(true);
    try {
      const nextGame = withStoredTokens(await getGame(gameId));
      setGame(nextGame);
      setGameMode(nextGame.mode);
      if (nextGame.humanColor) {
        setHumanColor(nextGame.humanColor);
      }
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

  async function createGame(mode: GameMode, color: StoneColor) {
    setBusy(true);
    try {
      const nextGame = await api<GameState>('/api/games', {
        method: 'POST',
        body: JSON.stringify(mode === 'human-agent' ? { mode, humanColor: color } : { mode }),
      });
      saveTokens(nextGame.gameId, {
        humanToken: nextGame.humanToken || '',
        agentToken: nextGame.agentToken || '',
        agentBlackToken: nextGame.agentBlackToken || '',
        agentWhiteToken: nextGame.agentWhiteToken || '',
      });
      setGame(nextGame);
      setGameMode(mode);
      if (nextGame.humanColor) {
        setHumanColor(nextGame.humanColor);
      }
      setCopiedPrompt('');
      setShowResignConfirm(false);
      setMessage('');
      await refreshHistory();
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setBusy(false);
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
                    return (
                      <button
                        key={`${row}-${col}`}
                        className={`cell ${isLast ? 'last' : ''} ${isWin ? 'winning' : ''}`}
                        aria-label={`${row},${col}${cell ? ` ${colorLabel(cell)}` : ''}`}
                        disabled={!canHumanMove || Boolean(cell)}
                        onClick={() => void placeHumanMove(row, col)}
                      >
                        {cell ? <span className={`stone ${cell}`} /> : <span className="hover-stone" />}
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
              <h2>新棋局</h2>
            </div>
            <div className="segmented" aria-label="选择对战模式">
              <button
                className={gameMode === 'human-agent' ? 'active' : ''}
                onClick={() => setGameMode('human-agent')}
                disabled={busy}
              >
                人机
              </button>
              <button
                className={gameMode === 'agent-agent' ? 'active' : ''}
                onClick={() => setGameMode('agent-agent')}
                disabled={busy}
              >
                机机
              </button>
            </div>
            {gameMode === 'human-agent' ? (
              <div className="segmented" aria-label="选择人类棋色">
                <button
                  className={humanColor === 'black' ? 'active' : ''}
                  onClick={() => setHumanColor('black')}
                  disabled={busy}
                >
                  我执黑
                </button>
                <button
                  className={humanColor === 'white' ? 'active' : ''}
                  onClick={() => setHumanColor('white')}
                  disabled={busy}
                >
                  我执白
                </button>
              </div>
            ) : null}
            <button className="primary-button" onClick={() => void createGame(gameMode, humanColor)} disabled={busy}>
              <Plus size={18} />
              <span>{gameMode === 'agent-agent' ? '创建机机局' : '创建人机局'}</span>
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
            <div className="history-list">
              {history.map((item) => (
                <button
                  key={item.gameId}
                  className={`history-row ${item.gameId === game?.gameId ? 'selected' : ''}`}
                  onClick={() => void loadGame(item.gameId)}
                >
                  <span>{shortId(item.gameId)}</span>
                  <span>{historyLabel(item)}</span>
                </button>
              ))}
            </div>
          </section>

          <section className="control-block moves-block">
            <div className="section-title">
              <Clipboard size={18} />
              <h2>落子</h2>
            </div>
            <ol className="move-list">
              {(game?.moves || []).slice(-12).map((move) => (
                <li key={move.moveNumber}>
                  <span>{move.moveNumber}</span>
                  <strong>{colorLabel(move.color)}</strong>
                  <span>{playerLabel(move.player, game || undefined)}</span>
                  <span>
                    {move.row},{move.col}
                  </span>
                </li>
              ))}
            </ol>
          </section>

          {message ? <div className="message">{message}</div> : null}
        </aside>
      </main>

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

function listGames() {
  return api<GameListItem[]>('/api/games?limit=20');
}

function getGame(gameId: string) {
  return api<GameState>(`/api/games/${gameId}`);
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

function buildAgentPrompt(game: GameState, role: AgentRole, token: string) {
  const baseUrl = configuredApiBase || window.location.origin;
  const color = agentColorForRole(game, role);
  const opponent = game.mode === 'agent-agent' ? oppositeAgentLabel(role) : `人类（${colorLabel(game.humanColor)}）`;
  const expectedTurn = role;
  const waitTarget = game.mode === 'agent-agent' ? '另一位 Agent' : '人类';
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
- 本局没有禁手规则。

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
      { label: '手数', value: String(game.moveCount) },
    ];
  }
  return [
    { label: '棋局', value: shortId(game.gameId) },
    { label: '模式', value: '人机对战' },
    { label: '我方', value: colorLabel(game.humanColor) },
    { label: 'Agent', value: colorLabel(game.agentColor) },
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
