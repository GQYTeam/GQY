/* 顾清影 · 本地回复生成（演示用文案与产物提取） */
(() => {
  'use strict';
  const G = (window.GQY = window.GQY || {});

  function artifactFrom(text, title, dir) {
    const m = String(text || '').match(/```([\w+-]*)\n([\s\S]*?)```/);
    if (!m) return null;
    const content = m[2].replace(/\n$/, '');
    return {
      title: title,
      path: (dir || '~/项目') + '/' + title,
      lang: m[1] || 'code',
      content: content,
      lines: content.split('\n').length
    };
  }

  function greetingAnswer() {
    return '你好，我在。今天想从哪里开始？\n\n可以给我一个具体目标，比如「把这个页面改成深色主题」；也可以先丢一段材料过来，我帮你判断下一步。';
  }

  function aboutAnswer() {
    return [
      '这是一个纯前端的对话演示，没有服务端：',
      '',
      '- 消息保存在浏览器的 `localStorage` 里，刷新不会丢；',
      '- 回复由本地脚本模拟，延迟和打字效果都是演出来的，不会联网；',
      '- 切换工作区、删除对话、搜索、模型与推理强度都会真实地改变本地状态。',
      '',
      '想接真实模型时，只需要把 `generateReply()` 换成一次流式 `fetch`，其余渲染、滚动、停止生成都不必改。',
      '',
      '```js',
      'const res = await fetch("/api/chat", {',
      '  method: "POST",',
      '  headers: { "content-type": "application/json" },',
      '  body: JSON.stringify({ messages })',
      '});',
      '```',
      '',
      '需要我把接后端的那一层也写出来吗？'
    ].join('\n');
  }

  function realtimeAnswer() {
    return [
      '这个演示没有联网能力，拿不到实时数据。',
      '',
      '真实产品里，这一层通常放在服务端：浏览器调用自己的接口，由服务端请求天气或新闻源，再把结果流式回传。这样既能藏住密钥，也避开了跨域。',
      '',
      '```js',
      'app.get("/api/weather", async (req, res) => {',
      '  const upstream = await fetch(',
      '    "https://api.example.com/weather?city=" + encodeURIComponent(req.query.city)',
      '  );',
      '  res.setHeader("cache-control", "public, max-age=300");',
      '  res.json(await upstream.json());',
      '});',
      '```',
      '',
      '先告诉我你要接的是哪一类数据源，我可以把接口形状和错误处理一起定下来。'
    ].join('\n');
  }

  function codeAnswer(text) {
    const isPy = /(python|pytorch|pandas|numpy|dataset|dataloader|torch|脚本)/i.test(text);
    if (isPy) {
      return [
        '可以。下面这版把「读什么、怎么校验、怎么用」拆开，先保证能跑通：',
        '',
        '```python',
        'from pathlib import Path',
        'import csv',
        '',
        'def load_rows(path: str) -> list[dict]:',
        '    """读取 CSV，忽略空行，顺手去掉首尾空白。"""',
        '    with Path(path).open(encoding="utf-8", newline="") as f:',
        '        reader = csv.DictReader(f)',
        '        return [',
        '            {k: (v or "").strip() for k, v in row.items()}',
        '            for row in reader',
        '            if any((v or "").strip() for v in row.values())',
        '        ]',
        '',
        'rows = load_rows("data/sample.csv")',
        'print(len(rows), rows[:1])',
        '```',
        '',
        '几个值得提前定下来的点：',
        '',
        '- **编码**：显式写 `utf-8` 和 `newline=""`，避免 Windows 与 macOS 行为不一致。',
        '- **坏行**：这里选择跳过空行；业务数据建议把坏行收集起来返回，不要静默丢掉。',
        '- **大文件**：超过几十万行时改成生成器逐个 `yield`，不要一次性塞进内存。',
        '',
        '把你的真实字段名和文件结构发我，我可以把这版改成能直接接进项目的实现。'
      ].join('\n');
    }

    if (/(react|hook|组件|vue|前端)/i.test(text)) {
      return [
        '可以，先写一个最小可用的版本。关键是把「输入」和「副作用」分开，组件只关心结果：',
        '',
        '```jsx',
        'import { useEffect, useState } from "react";',
        '',
        'export function useDebouncedValue(value, delay = 300) {',
        '  const [debounced, setDebounced] = useState(value);',
        '',
        '  useEffect(() => {',
        '    const timer = setTimeout(() => setDebounced(value), delay);',
        '    return () => clearTimeout(timer);',
        '  }, [value, delay]);',
        '',
        '  return debounced;',
        '}',
        '```',
        '',
        '使用的时候，把搜索请求挂在 `debounced` 上，而不是原始的输入值：',
        '',
        '```jsx',
        'const query = useDebouncedValue(input, 350);',
        'useEffect(() => {',
        '  if (!query) return;',
        '  fetch("/api/search?q=" + encodeURIComponent(query));',
        '}, [query]);',
        '```',
        '',
        '两个细节：`delay` 变化时要重新计时，所以它也在依赖数组里；请求要支持取消，否则快速输入时旧结果会覆盖新结果。',
        '',
        '需要我补上 AbortController 的版本吗？'
      ].join('\n');
    }

    return [
      '先用一个足够小的版本把问题拆开，再决定要不要抽象。',
      '',
      '```js',
      'function debounce(fn, wait = 300) {',
      '  let timer;',
      '  return function debounced(...args) {',
      '    clearTimeout(timer);',
      '    timer = setTimeout(() => fn.apply(this, args), wait);',
      '  };',
      '}',
      '```',
      '',
      '用它包住输入事件，搜索请求就只会在用户停下来之后发一次：',
      '',
      '```js',
      'const onInput = debounce((value) => {',
      '  fetch("/api/search?q=" + encodeURIComponent(value));',
      '}, 350);',
      '',
      'input.addEventListener("input", (e) => onInput(e.target.value));',
      '```',
      '',
      '几个容易踩的点：',
      '',
      '- 需要 `this` 时用普通函数，不要用箭头函数；',
      '- 组件卸载或页面切换时要 `clearTimeout`，否则会更新已经销毁的界面；',
      '- 需要「立即执行一次再防抖」的场景，加一个 leading 选项，不要另写一份。',
      '',
      '需要我按你现在的框架补完整版本吗？'
    ].join('\n');
  }

  function designAnswer() {
    return [
      '先把方向收窄成三个决定，再动笔画界面：',
      '',
      '| 决定 | 建议 | 原因 |',
      '| --- | --- | --- |',
      '| 底色 | 暖白 `#faf9f4` | 比纯白柔和，长时间阅读不刺眼 |',
      '| 强调色 | 赤陶 `#d97757` | 只出现在图标、发送按钮和链接上，面积越小越有效 |',
      '| 字体 | 一个衬线 + 一个无衬线 | 衬线负责标题与引文，无衬线负责操作界面 |',
      '',
      '交互上只保留两处动效：首屏标题的淡入，以及流式输出时的光标。其余 hover 一律用底色变化，不加位移和阴影。',
      '',
      '最后检查两件事：正文每行不超过 80 个字符；所有可点区域的对比度至少 4.5:1。',
      '',
      '要我按这个方向直接落成一套颜色变量和一版静态稿吗？'
    ].join('\n');
  }

  function explainAnswer(text) {
    const topic = (text.replace(/^(解释一下|解释|总结一下|总结|什么是|为什么)/, '').replace(/[？?。！!]/g, '').trim() || '这个问题').slice(0, 24);
    return [
      '## 先说结论',
      '',
      '「' + topic + '」可以先理解成：**在明确的约束下，把输入稳定地变成可预期输出的一套做法**。它的价值不在概念本身，而在于替使用者省掉每次重新判断的成本。',
      '',
      '### 判断它是否适用',
      '',
      '1. **输入是否稳定** —— 变化过于随机的场景，收益有限。',
      '2. **失败是否可承受** —— 能回滚、能重试，就值得先跑起来。',
      '3. **边界是否清晰** —— 边界模糊时，先补定义，再谈实现。',
      '',
      '### 一个常见误区',
      '',
      '把它当成一次性的大改造。更稳的做法是找一个足够小的切口，先用一周验证，再扩大范围。',
      '',
      '如果你愿意说说具体场景，我可以把上面的判断换成对你这种情况的取舍。'
    ].join('\n');
  }

  function generalAnswer(text) {
    const topic = (text.replace(/[？?。！!]/g, '').trim() || '这件事').slice(0, 22);
    return [
      '收到。先把「' + topic + '」拆成能落地的三步：',
      '',
      '1. **定边界**：明确这次要解决的具体场景，以及明确不做什么。范围越小，结果越容易被验证。',
      '2. **做最小可用版**：先跑通一条最短路径，允许它粗糙，但不允许它不可运行。',
      '3. **补细节**：根据真实使用中的卡点再补异常、文案和样式，不要在第一步就穷举。',
      '',
      '如果是我来做，会先花半天把第 1 步写成一句话，再开始动手。你希望我从哪一步开始？'
    ].join('\n');
  }

  function generateReply(prompt, files) {
    const text = String(prompt || '').trim();
    let reply;
    let thinking;
    let tools = [];
    let artifactTitle = '';

    if (/^(你好|您好|hi|hello|嗨|在吗|hey)[!！。,.，\s]*$/i.test(text)) {
      reply = greetingAnswer();
      thinking = '只是一句问候，不需要铺垫背景。\n给一个具体、可执行的开场，把选择权交回用户。';
    } else if (/(你是谁|能做什么|怎么用|这个页面|这个演示|demo|本地脚本|localStorage)/i.test(text)) {
      reply = aboutAnswer();
      thinking = '用户在问这个演示本身。要讲清三件事：\n- 没有服务端\n- 消息存在 localStorage\n- 回复由本地脚本生成\n最后给一个接真实模型的入口。';
      tools = [{ name: '查看源码', detail: 'app.js', icon: 'i-folder', args: [['path', 'app.js']], output: '无外部依赖 · 单一入口' }];
    } else if (/(天气|气温|新闻|股价|实时|今天.*怎么样)/.test(text)) {
      reply = realtimeAnswer();
      thinking = '这个问题需要实时数据，但演示环境没有联网能力。\n先说明边界，再给服务端转发的写法，避免暴露密钥。';
      tools = [{ name: '联网检索', detail: 'api.example.com', icon: 'i-bolt', args: [['url', 'api.example.com/weather']], output: '演示环境未联网，已跳过' }];
    } else if (/(设计|配色|排版|界面|ui|交互|视觉)/i.test(text)) {
      reply = designAnswer();
      thinking = '先把方向收窄成三个决定：底色、强调色、字体。\n动效只保留首屏淡入和流式光标，其余保持克制。';
      tools = [{ name: '读取文件', detail: 'styles.css', icon: 'i-folder', args: [['path', 'styles.css']], output: '24 个颜色变量' }];
    } else if (/(代码|函数|实现|写个|写一个|bug|报错|react|vue|python|dataloader|dataset|pytorch|接口|api|html|css|js)/i.test(text)) {
      reply = codeAnswer(text);
      const isPy = /(python|pytorch|pandas|numpy|dataset|dataloader|torch|脚本)/i.test(text);
      artifactTitle = isPy ? 'load_rows.py' : /(react|hook|组件|vue)/i.test(text) ? 'useDebouncedValue.jsx' : 'debounce.js';
      thinking = isPy
        ? '要一个能直接跑的最小实现：读 CSV、跳过空行。\n把编码、坏行、大文件三个边界提前说清楚。'
        : '用防抖把请求挂在稳定值上。\n注意 delay 变化要重新计时，请求要可取消，否则旧结果会覆盖新结果。';
      tools = isPy
        ? [{ name: '读取文件', detail: 'data/sample.csv', icon: 'i-folder',
             args: [['path', 'data/sample.csv'], ['encoding', 'utf-8']],
             output: '42 行 · UTF-8 · 表头 5 列' },
           { name: '运行代码', detail: 'python check.py', icon: 'i-bolt',
             args: [['cmd', 'python check.py']],
             output: '42 行读取成功\n0 个坏行' }]
        : [{ name: '运行代码', detail: 'node debounce.test.js', icon: 'i-bolt',
             args: [['cmd', 'node debounce.test.js']],
             output: '3 个断言通过' }];
    } else if (/(什么是|区别|原理|解释|总结|为什么)/.test(text)) {
      reply = explainAnswer(text);
      thinking = '结论放最前面，再给三条判断标准和一个常见误区，避免堆概念。';
    } else {
      reply = generalAnswer(text);
      thinking = '问题比较开放，先拆成定边界、做最小版本、补细节三步，再问用户从哪一步开始。';
    }

    if (files && files.length) {
      const names = files.slice(0, 3).map((n) => '「' + n + '」').join('、');
      reply = '收到你附带的 ' + files.length + ' 个文件：' + names + (files.length > 3 ? ' 等' : '') +
        '。演示环境不会真正上传，这里按文件名理解你的意图。\n\n' + reply;
      thinking += '\n用户还附带了文件，但演示里不会真的读取，按文件名推测意图。';
      tools = tools.concat([{ name: '读取附件', detail: files[0], icon: 'i-paperclip',
        args: [['file', files[0]], ['count', String(files.length)]],
        output: files.length + ' 个文件 · 模拟解析' }]);
    }

    return { thinking, tools, text: reply, artifact: artifactTitle ? artifactFrom(reply, artifactTitle) : null, question: null };
  }

  Object.assign(G, { artifactFrom, generateReply });
})();
