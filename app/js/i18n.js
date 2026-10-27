/* 顾清影 · 轻量 i18n：导航、看板、引导与设置标签 */
(() => {
  'use strict';
  const G = window.GQY;
  const app = G.app;

  const DICT = {
    zh: {
      'nav.newChat': '新会话',
      'nav.workspace': '工作区',
      'nav.search': '搜索对话',
      'nav.board': '看板',
      'nav.extensions': '扩展',
      'composer.placeholder': '发消息给顾清影…',
      'board.title': '看板',
      'board.desc': '把当前工作区要做的事放在这里；卡片可以关联会话。',
      'board.addPlaceholder': '添加一张卡片…',
      'board.add': '添加',
      'board.todo': '待办',
      'board.doing': '进行中',
      'board.done': '已完成',
      'board.empty': '暂无卡片',
      'onboard.title': '开始使用顾清影',
      'onboard.desc': '先定三件事，之后都能在设置里改。',
      'onboard.workspace': '工作区名称',
      'onboard.model': '默认模型（可留空）',
      'onboard.motion': '减少动效',
      'onboard.skip': '跳过',
      'onboard.start': '开始使用',
      'settings.language': '语言',
      'tab.general': '设置',
      'tab.models': '模型 & 供应商',
      'tab.personas': '人设 & 预设',
      'tab.memory': '记忆',
      'tab.knowledge': '知识库',
      'tab.voice': '语音',
      'tab.packages': '软件包',
      'tab.tools': '工具',
      'tab.usage': '用量',
      'tab.accounts': '账户 & 权限'
    },
    en: {
      'nav.newChat': 'New chat',
      'nav.workspace': 'Workspace',
      'nav.search': 'Search chats',
      'nav.board': 'Board',
      'nav.extensions': 'Extensions',
      'composer.placeholder': 'Message Gu Qingying…',
      'board.title': 'Board',
      'board.desc': 'Keep tasks for this workspace here; cards can link to a conversation.',
      'board.addPlaceholder': 'Add a card…',
      'board.add': 'Add',
      'board.todo': 'To do',
      'board.doing': 'Doing',
      'board.done': 'Done',
      'board.empty': 'No cards',
      'onboard.title': 'Welcome to Gu Qingying',
      'onboard.desc': 'Three things to start; all can be changed later.',
      'onboard.workspace': 'Workspace name',
      'onboard.model': 'Default model (optional)',
      'onboard.motion': 'Reduce motion',
      'onboard.skip': 'Skip',
      'onboard.start': 'Get started',
      'settings.language': 'Language',
      'tab.general': 'Settings',
      'tab.models': 'Models & providers',
      'tab.personas': 'Personas & presets',
      'tab.memory': 'Memory',
      'tab.knowledge': 'Knowledge',
      'tab.voice': 'Voice',
      'tab.packages': 'Packages',
      'tab.tools': 'Tools',
      'tab.usage': 'Usage',
      'tab.accounts': 'Account & access'
    }
  };

  function locale() {
    return app.state.locale === 'en' ? 'en' : 'zh';
  }

  function t(key) { return DICT[locale()][key] || key; }

  function apply() {
    const dict = DICT[locale()];
    document.documentElement.lang = locale() === 'en' ? 'en' : 'zh-CN';
    document.querySelectorAll('[data-i18n]').forEach((node) => {
      const key = node.getAttribute('data-i18n');
      if (dict[key]) node.textContent = dict[key];
    });
    document.querySelectorAll('[data-i18n-placeholder]').forEach((node) => {
      const key = node.getAttribute('data-i18n-placeholder');
      if (dict[key]) node.setAttribute('placeholder', dict[key]);
    });
    if (app.el.localeSelect) app.el.localeSelect.value = locale();
    if (app.renderBoard) app.renderBoard();
  }

  G.i18n = { apply, t, dict: DICT, locale };
})();
