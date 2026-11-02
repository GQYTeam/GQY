import { esc } from "./markdown.js";
import { loadPref, savePref, uid } from "./store.js";
import { openOverlay, toast } from "./ui.js";
import { $ } from "./render.js";

const KEY = "gqy.board";
const COLUMNS = [
  ["todo", "待办"],
  ["doing", "进行中"],
  ["done", "完成"],
];

let board = null;
let bound = false;

export function initBoard() {
  if (bound) return;
  bound = true;
  $("#openBoard")?.addEventListener("click", () => openBoard());
  const body = $("#boardBody");
  body?.addEventListener("click", onClick);
  body?.addEventListener("submit", onSubmit);
}

function loadBoard() {
  try {
    const raw = loadPref(KEY, "");
    if (raw) {
      const parsed = JSON.parse(raw);
      if (parsed && COLUMNS.every(([id]) => Array.isArray(parsed[id]))) return parsed;
    }
  } catch {
    /* fall through to the sample board */
  }
  return {
    todo: [
      { id: uid("card"), text: "把 providers.json 换成两个账号" },
      { id: uid("card"), text: "给路由组加 usage 策略" },
    ],
    doing: [{ id: uid("card"), text: "跑通 /v1/messages 流式" }],
    done: [{ id: uid("card"), text: "配置热重载与失败回滚" }],
  };
}

function persist() {
  try {
    savePref(KEY, JSON.stringify(board));
  } catch {
    /* board is a local convenience */
  }
}

function openBoard() {
  if (!board) board = loadBoard();
  renderBoard();
  openOverlay("boardOverlay");
}

function renderBoard() {
  const body = $("#boardBody");
  if (!body) return;
  body.innerHTML = `<p class="field-hint" style="margin:0 0 14px">这里的卡片只存在当前浏览器。示例卡可以直接删。</p>
    <div class="board-grid">
      ${COLUMNS.map(([id, label]) => `<section class="board-col">
        <div class="board-col-head"><span>${label}</span><span>${board[id].length}</span></div>
        <div class="board-cards">
          ${board[id]
            .map(
              (card) => `<article class="board-card">
                <div>${esc(card.text)}</div>
                <div class="board-card-actions">
                  ${id !== "todo" ? `<button type="button" data-board-move="${id}" data-board-to="todo" data-board-id="${card.id}" aria-label="移到待办">←</button>` : ""}
                  ${id !== "done" ? `<button type="button" data-board-move="${id}" data-board-to="${id === "todo" ? "doing" : "done"}" data-board-id="${card.id}" aria-label="向前移动">→</button>` : ""}
                  <button type="button" data-board-delete="${id}" data-board-id="${card.id}" aria-label="删除卡片">×</button>
                </div>
              </article>`,
            )
            .join("")}
        </div>
        <form class="board-add" data-board-add="${id}">
          <input name="text" placeholder="加一张卡…" aria-label="新卡片内容">
          <button type="submit">＋</button>
        </form>
      </section>`).join("")}
    </div>`;
}

function onClick(event) {
  const move = event.target.closest("[data-board-move]");
  if (move) {
    const from = move.dataset.boardMove;
    const to = move.dataset.boardTo;
    const id = move.dataset.boardId;
    const index = board[from].findIndex((card) => card.id === id);
    if (index !== -1) {
      const [card] = board[from].splice(index, 1);
      board[to].push(card);
      persist();
      renderBoard();
    }
    return;
  }
  const remove = event.target.closest("[data-board-delete]");
  if (remove) {
    const id = remove.dataset.boardId;
    const column = remove.dataset.boardDelete;
    board[column] = board[column].filter((card) => card.id !== id);
    persist();
    renderBoard();
  }
}

function onSubmit(event) {
  const form = event.target.closest("[data-board-add]");
  if (!form) return;
  event.preventDefault();
  const input = form.querySelector("input");
  const text = input.value.trim();
  if (!text) return;
  const column = form.dataset.boardAdd;
  board[column].push({ id: uid("card"), text });
  persist();
  renderBoard();
  toast("卡片已添加");
}
