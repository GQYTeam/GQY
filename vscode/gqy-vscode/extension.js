// 顾清影 · VSCode 扩展
// 两件事：把 WebUI 嵌进编辑器面板；把当前文件/选区发给她。
// 不重写聊天界面 —— 面板里就是原本那个 WebUI。
const vscode = require('vscode');
const http = require('http');

const cfg = () => vscode.workspace.getConfiguration('gqy');
const port = () => cfg().get('port', 4096);

let panel;

function open() {
  // ?panel=1：WebUI 自带的嵌入形态，隐藏侧栏/顶栏/遮罩，只剩聊天本体
  const url = `http://127.0.0.1:${port()}/?panel=1`;
  if (panel) {
    panel.reveal(vscode.ViewColumn.Beside);
    return;
  }
  panel = vscode.window.createWebviewPanel('gqy', '顾清影', vscode.ViewColumn.Beside, {
    enableScripts: true,
    retainContextWhenHidden: true,
  });
  // iframe 里就是原本那个 WebUI。加载不出来时显示原因，不留白屏。
  // frame-src * 是必须的：webview 默认 CSP 会挡掉 iframe（抄自内置 Simple Browser）。
  panel.webview.html = `<!DOCTYPE html>
<html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; frame-src *;">
<style>
  html,body{margin:0;padding:0;height:100vh;overflow:hidden}
  iframe{border:0;width:100%;height:100vh;display:block}
  #tip{font:13px/1.7 -apple-system,sans-serif;padding:24px;color:var(--vscode-foreground)}
  #tip code{background:var(--vscode-textCodeBlock-background);padding:1px 5px;border-radius:3px}
  a{color:var(--vscode-textLink-foreground)}
</style></head>
<body>
  <div id="tip">正在连接顾清影 <code>${url}</code>…</div>
  <iframe id="f" style="display:none" src="${url}"
    sandbox="allow-scripts allow-forms allow-same-origin allow-downloads"
    allow="clipboard-read; clipboard-write; microphone"></iframe>
  <script>
    const f = document.getElementById('f'), tip = document.getElementById('tip');
    f.addEventListener('load', () => { tip.remove(); f.style.display = 'block'; });
    setTimeout(() => {
      if (!document.getElementById('tip')) return;
      tip.innerHTML = '连不上顾清影。<br><br>'
        + '1. 确认她在跑：打开 GQYApp，或终端 <code>gqy web</code><br>'
        + '2. 端口对不对：设置里的 <code>gqy.port</code>（当前 ${port()}）<br>'
        + '3. 还是白屏就点这里用浏览器打开：<a href="${url}">${url}</a>';
    }, 4000);
  </script>
</body></html>`;
  panel.onDidDispose(() => { panel = undefined; });
}

// 只发路径，不发全文：她自己有 read_file / grep / glob。
function contextLines() {
  const ed = vscode.window.activeTextEditor;
  if (!ed) return '';
  const sel = ed.document.getText(ed.selection);
  const root = vscode.workspace.getWorkspaceFolder(ed.document.uri)?.uri.fsPath;
  let out = '';
  if (root) out += `\n项目根目录：${root}\n`;
  out += `当前文件：${ed.document.uri.fsPath}（第 ${ed.selection.active.line + 1} 行）\n`;
  if (sel) out += `\n选中的代码：\n\`\`\`\n${sel}\n\`\`\`\n`;
  return out;
}

function post(content) {
  const body = JSON.stringify({ content, mode: cfg().get('mode', 'normal') });
  return new Promise((resolve, reject) => {
    const req = http.request(
      { host: '127.0.0.1', port: port(), path: '/api/turns', method: 'POST',
        headers: { 'content-type': 'application/json', 'content-length': Buffer.byteLength(body) } },
      (res) => {
        res.resume();
        res.statusCode < 300 ? resolve() : reject(new Error(`HTTP ${res.statusCode}`));
      },
    );
    req.on('error', reject);
    req.end(body);
  });
}

async function ask() {
  const q = await vscode.window.showInputBox({
    prompt: '想问顾清影什么？',
    placeHolder: '这段为什么 panic？',
  });
  if (!q) return;
  open();
  try {
    await post(q + '\n' + contextLines());
  } catch (err) {
    vscode.window.showErrorMessage(`顾清影没在跑（${err.message}）：打开 GQYApp，或终端执行 gqy web`);
  }
}

function activate(ctx) {
  const bar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
  bar.text = '$(comment-discussion) 顾清影';
  bar.tooltip = '打开顾清影（⌥⌘J）';
  bar.command = 'gqy.open';
  bar.show();
  ctx.subscriptions.push(
    bar,
    vscode.commands.registerCommand('gqy.open', open),
    vscode.commands.registerCommand('gqy.ask', ask),
  );
}

module.exports = { activate, deactivate() {} };
