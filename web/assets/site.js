/* iamcsq.top shared English/Chinese UI. User-authored content stays in its original language. */
(() => {
  'use strict';
  const pairs = [
    ['首页','Home'],['全站主导航','Main navigation'],['csq 个人网站首页','csq personal website home'],['返回个人首页','Back to personal home'],['查看个人项目','Explore projects'],['给我发邮件','Email me'],['进入 Digital Twin','Explore Digital Twin'],['进入数字分身','Explore Digital Twin'],['打开 WeUtil','Open Utils'],['打开 Utils','Open Utils'],['查看 Trends','Explore Trends'],['个人项目','Selected projects'],['关注方向','Focus areas'],['架构设计','System architecture'],['AI 应用','AI applications'],['数据平台','Data platforms'],['用代码构建，用长期主义观察','Build with code. Observe for the long term.'],
    ['构建会思考的系统，','Systems that think.'],['也记录长期的变化。','Ideas that evolve.'],['你好，我是 csq。这里是我的个人网站：用 Digital Twin 整理知识与记忆，用 Trends 观察公司，用 Blogs 记录思考，用 Utils 提升开发效率。','I’m csq. This is my personal home for knowledge and memory, long-term research, writing, and focused developer tools.'],['四个模块共享同一套导航、设计语言和访问入口。','Four modules share one navigation, design language and personal home.'],['知识、记忆与个人复盘','Knowledge, memory and reflection'],['基本面与长期趋势','Fundamentals and long-term trends'],['JSON 与 HTTP 工具','JSON and HTTP utilities'],['整理个人知识、检索记忆，也能结合过往记录一起复盘。回答附有引用时，可以回查原文。','Organize personal knowledge, retrieve memories and reflect on past records, with source citations when available.'],['帮我复盘最近关注的技术方向。','Review the technical directions I’ve been exploring.'],['可以从你的知识库与过往记录中整理。','I can synthesize them from your knowledge and past records.'],['用趋势理解世界','Understand the world through trends'],['结合商业模式、财务表现、估值与披露来源，跟踪公司的长期变化。','Track long-term business change through fundamentals, valuation and primary disclosures.'],['顺手的开发者工具','Focused developer tools'],['只保留最常用的两个工具：JSON 格式化与响应对比，以及 HTTP 请求调试。','Two focused tools: JSON formatting and comparison, plus HTTP request debugging.'],['关注服务边界、数据流和系统可维护性。','Service boundaries, data flow and maintainable systems.'],['探索 AI 在知识检索、对话和个人复盘中的用法。','AI for knowledge retrieval, conversation and personal reflection.'],['关注数据采集、整理和分析，以及如何用数据跟踪变化。','Data collection, modeling and analysis for tracking change.'],
    ['登录 · Echo of CSQ','Sign in · Echo of CSQ'],['与个人知识、记忆和过往记录对话。登录后进入你的私有工作区。','Talk with your knowledge, memories and past records in a private workspace.'],['欢迎回来','Welcome back'],['单用户私有部署 · 登录后开始对话','Private single-user deployment · Sign in to start'],['用户名','Username'],['密码','Password'],['登录','Sign in'],['登录中…','Signing in…'],['会话通过安全 Cookie 维护；知识库与对话仅属于当前账号。','Your session is secured with cookies. Knowledge and conversations belong only to your account.'],['尝试过于频繁，请稍后再试','Too many attempts. Please try again later.'],['用户名或密码错误','Incorrect username or password'],['网络错误，请重试','Network error. Please try again.'],
    ['知识库','Knowledge'],['画像','Profile'],['我的画像','My profile'],['记忆','Memories'],['新对话','New chat'],['登出','Sign out'],['最近会话','Recent conversations'],['发送','Send'],['说点什么…（Enter 发送，Shift+Enter 换行）','Write something… (Enter to send, Shift+Enter for a new line)'],['我记忆与风格的回声','An echo of my memory and style'],['返回csq个人主页','Back to csq personal home'],['网页 URL（http/https）','Web URL (http/https)'],['导入','Import'],['上传入库','Upload'],['支持 TXT / Markdown / HTML（含网页 URL），自动分块并向量化；入库需要几秒到几十秒。','TXT, Markdown and HTML files or URLs are chunked and vectorized. Indexing may take a few seconds.'],['提交日记','Publish post'],['写写今天做了什么、想了什么、怎么决策的…','Write an idea, essay or technical note…'],['提交后会自动抽取成记忆（事实/情景/方法）','Publishing indexes the post and distills it into memories.'],['每次提交都会抽取记忆；画像每天凌晨 02:30 自动重建。','Posts feed knowledge and memory. Your profile rebuilds automatically every night.'],['近期日记处理记录','Recent blog learning jobs'],['重建画像','Rebuild profile'],['正在重建画像…','Rebuilding profile…'],['画像已重建','Profile rebuilt'],['还没有画像数据。画像每天凌晨 02:30 自动生成；也可以点「重建画像」立即根据已有记忆生成。','No profile yet. It rebuilds nightly, or you can rebuild it now from existing memories.'],['身份','Identity'],['性格','Personality'],['语气','Tone'],['价值观','Values'],['口头禅','Catchphrases'],['禁忌','Boundaries'],['知识版图','Knowledge map'],['当前关注','Current focus'],['进行中','Active projects'],['近期状态','Recent state'],['近期变化','Recent updates'],['搜索记忆','Search memories'],['搜索记忆正文','Search memory text'],['搜索','Search'],['加载更多','Load more'],['正在读取记忆…','Loading memories…'],['没有找到记忆','No memories found'],['已归档','Archived'],['待确认','Pending confirmation'],['已纠正','Corrected'],['来源','Source'],['打开原网页','Open original page'],['当前记忆','Current memory'],['保存纠正','Save correction'],['归档','Archive'],['重试向量同步','Retry vector sync'],['向量同步失败，可重试','Vector sync failed. Retry available.'],['关闭','Close'],['重试','Retry'],['删除失败','Delete failed'],['删除','Delete'],['正在加载…','Loading…'],['暂无会话','No conversations yet'],['新会话','New conversation'],['正在思考…','Thinking…'],['回答生成失败','Response generation failed'],['知识库为空','Knowledge base is empty'],['未找到相关知识','No related knowledge found'],['上传失败','Upload failed'],['导入失败','Import failed'],['文章标题','Post title'],['输入文章标题','Enter a post title'],['发布文章','Publish post'],['发布中…','Publishing…'],['文章已发布，正在学习并写入知识库','Post published. Learning and knowledge indexing are underway.'],['标题和正文不能为空','Title and content are required'],['近期文章学习状态','Recent blog learning jobs'],['公开文章','Public post'],['记忆学习','Memory learning'],['知识索引','Knowledge indexing'],['查看公开文章','View public post'],['写博客','Write a post'],
    ['真实数据','Real data'],['研究工作台','Research workspace'],['用趋势理解世界。','Understand the world through trends.'],['全部财务数值为 Mock 模拟数据','All financial values are simulated mock data'],['趋势发现','Trend discovery'],['选择股票','Select a stock'],['搜索股票名称或代码','Search stock name or symbol'],['收起股票列表','Collapse stock list'],['展开股票列表','Expand stock list'],['可选股票','Available stocks'],['正在加载股票…','Loading stocks…'],['股票列表加载失败，请重新打开下拉框','Unable to load stocks. Reopen the list to retry.'],['没有匹配的股票','No matching stocks'],['正在加载数据…','Loading data…'],['暂无可用数据','No data available'],['数据加载失败','Failed to load data'],['港股','HK stock'],['A 股','A-share'],['公司业务','Company overview'],['商业模式','Business model'],['目标用户','Target users'],['资料来源','Source'],['下一期业绩预期','Upcoming earnings estimates'],['财年','FY'],['全年','Full year'],['半年','Half year'],['截至','ending'],['预计同比','Estimated YoY'],['预计转盈','Expected to turn profitable'],['预计盈亏平衡','Expected to break even'],['预计减亏','Expected narrowing loss'],['预计亏损扩大','Expected widening loss'],['预计亏损持平','Expected flat loss'],['预计营收','Estimated revenue'],['预计利润','Estimated profit'],['全年预期减前三季度实绩','Full-year estimate less first three quarters'],['机构预期','Consensus estimate'],['全年预期覆盖','Full-year estimate covers'],['家机构','institutions'],['记录于','Recorded'],['暂无本财期预期','No estimate for this fiscal period'],['股价周线','Weekly price'],['近两年 · 每周最后一个交易日 · 未复权收盘价','Two years · Last trading day each week · Unadjusted close'],['暂无股价记录','No price records'],['突破','Breakout'],['跌破','Breakdown'],['已确认','Confirmed'],['回到原区间','Returned to range'],['反向越过原区间','Crossed opposite boundary'],['上沿','Upper'],['下沿','Lower'],['最大回撤起点','Drawdown peak'],['最大回撤低点','Drawdown trough'],['股价','Price'],['突破确认','Breakout confirmation'],['最近事件','Latest event'],['待确认','Pending confirmation'],['图中暂无突破或跌破','No breakout or breakdown in chart'],['需要至少','Requires at least'],['周收盘记录','weeks of closing records'],['突破标记图例','Breakout marker legend'],['确认','Confirmation'],['回到或反向越过原区间','Return or cross the original range'],['周最大回撤','-week maximum drawdown'],['在图上定位','Locate on chart'],['需要覆盖','Requires coverage of'],['个自然周的收盘记录','calendar weeks of closes'],['周均线','-week MA'],['均线趋势','Moving-average trend'],['收盘与均线呈多头排列','Bullish price and MA alignment'],['收盘与均线呈空头排列','Bearish price and MA alignment'],['收盘与均线方向未同步','Price and MAs are not aligned'],['动量（RSI）','Momentum (RSI)'],['处于超买区','Overbought'],['处于超卖区','Oversold'],['处于中性区','Neutral'],['为','is'],['超买 ≥ 70，超卖 ≤ 30','Overbought ≥ 70 · Oversold ≤ 30'],['收盘区间','Closing range'],['收盘突破区间上沿','Close broke above range'],['收盘跌破区间下沿','Close broke below range'],['收盘处于区间内','Close remains inside range'],['此前','Previous'],['周收盘范围','week closing range'],['周收益率波动','-week return volatility'],['最近','Recent'],['个周收盘收益率的标准差','weekly close-return standard deviation'],['周线技术洞察','Weekly technical insights'],['按有收盘记录的交易周计算。除权除息可能影响未复权数据的结果。','Calculated from weeks with closes. Corporate actions may affect unadjusted results.'],['需要','Requires'],['周收盘记录，当前有','weeks of records; currently'],['周。','weeks.'],['近 3 年','Last 3 years'],['至','to'],['营收与利润','Revenue & profit'],['单季营业收入与归母净利润','Quarterly revenue and net income'],['营收','Revenue'],['暂无可核实的单季营收','No verified quarterly revenue'],['利润','Net income'],['暂无可核实的单季归母净利润','No verified quarterly net income'],['营收增长率与利润增长率','Revenue & profit growth'],['同币种单季同比，利润增长率要求基期利润为正','Same-currency quarterly YoY; profit growth requires a positive base'],['营收增长率','Revenue growth'],['暂无可比较的上年同期营收','No comparable prior-year revenue'],['利润增长率','Profit growth'],['暂无可比较的上年同期盈利数据','No comparable profitable base period'],['倍','x'],['季末市盈率（TTM），亏损时留空','Quarter-end PE (TTM); blank when loss-making'],['暂无可用的季末 PE','No quarter-end PE available'],['股息率','Dividend yield'],['季末近 12 个月现金股息收益率','Trailing 12-month cash dividend yield at quarter end'],['暂无可核实的季末股息率','No verified quarter-end dividend yield'],['虚线为预期','Dashed line shows estimates'],['显示指标','Visible metrics'],['全部','All'],['预期','Estimate'],['亿元 CNY','CNY 100M'],['百万用户','million users'],['缺失','Missing'],['周线','Weekly'],['月度','Monthly'],['季度','Quarterly'],['年度','Annual'],['趋势图','trend chart'],['查看公司','View company'],['观察事实','Observed fact'],['规则','Rule'],['生成于','Generated'],['数据证据','Data evidence'],['参与规则判断的观测记录','Observations used by this rule'],['财务期间','Fiscal period'],['指标','Metric'],['数值','Value'],['来源','Source'],['计算版本','Calculation version'],['原始披露','Original disclosure'],['后续验证','Follow-up validation'],['下一财务期','Next fiscal period'],['按相同披露口径复核下一期的方向和幅度。','Verify direction and magnitude in the next period using the same disclosure basis.'],['披露','Disclosed'],['入库','Ingested'],['成长性','Growth'],['盈利能力','Profitability'],['现金流','Cash flow'],['财务健康','Financial health'],['估值','Valuation'],['经营数据','Operations'],
    ['少而顺手。只保留 JSON Lab 与 HTTP Client，并与 Digital Twin、Trends 共享统一的导航和体验。','Two focused tools with the same navigation and experience as the rest of csq’s personal website.'],['2 个工具','2 tools'],['开发者工具','Developer tools'],['格式化、校验、压缩和对比 JSON，保留精确比较与树形查看能力。','Format, validate, minify and compare JSON with precise diffing and tree exploration.'],['格式化与语法错误定位','Formatting and syntax error location'],['双响应 JSON 对比','Dual-response JSON comparison'],['树形展开、搜索与 JSONPath','Tree expansion, search and JSONPath'],['打开 JSON Lab →','Open JSON Lab →'],['调试 API 请求与响应，通过 iamcsq.top 同源代理发送请求，避免浏览器跨域限制。','Debug API requests and responses through the iamcsq.top same-origin proxy without browser CORS restrictions.'],['Method、Headers 与 Body 编辑','Method, headers and body editing'],['状态、响应头与响应体查看','Status, response headers and body inspection'],['服务端代理与安全校验','Server-side proxy and security validation'],['打开 HTTP Client →','Open HTTP Client →'],['工具','Tools'],['加载示例','Load example'],['清空响应','Clear responses'],['匹配唯一 id（忽略顺序）','Match unique ids (ignore order)'],['示例','Sample'],['格式化','Format'],['压缩','Minify'],['校验','Validate'],['排序键','Sort keys'],['复制','Copy'],['下载','Download'],['清空','Clear'],['输入','Input'],['输出','Output'],['粘贴或输入 JSON','Paste or type JSON'],['对比','Compare'],['树形','Tree'],['搜索键 / 值…','Search keys / values…'],['仅显示差异','Only show differences'],['交换','Swap'],['复制变更','Copy changes'],['准备对比','Ready to diff'],['节点','Nodes'],['深度','Depth'],['大小','Size'],['就绪','Ready'],['请求','Request'],['响应','Response'],['请求体','Body'],['认证','Auth'],['无','None'],['添加请求头','Add Header'],['添加字段','Add Field'],['发送请求','Send'],['历史记录','History'],['在此发送请求以查看响应','Send a request to see the response here'],['取消','Cancel'],['完成','Done'],['关闭全部','Close All'],['导入 cURL 命令','Import cURL Command'],['请求历史记录','Request History'],['清空全部','Clear All'],['请求通过 iamcsq.top 同源代理发送以避免 CORS','Requests go through the iamcsq.top same-origin proxy to bypass CORS'],
    ['写文章','Write a post'],['文章','Posts'],['正在加载文章…','Loading posts…'],['还没有发布文章。','No posts have been published yet.'],['文章加载失败，请稍后重试。','Unable to load posts. Please try again later.'],['阅读全文 →','Read post →'],['全部文章','All posts'],
    ['写作与思考','NOTES ON BUILDING & THINKING'],['关于 AI、工程、数据平台与长期学习的文章。发布的文章也会用于训练我的 Digital Twin。','Essays on AI, engineering, data platforms and long-term learning. Published posts also teach my Digital Twin.'],['关于 AI、工程、数据平台与长期学习的文章，用 Markdown 写作。发布的文章也会用于训练我的 Digital Twin。','Essays on AI, engineering, data platforms and long-term learning, written in Markdown. Published posts also teach my Digital Twin.'],['编辑','Edit'],['目录','Contents'],['博客 × 数字分身','BLOGS × DIGITAL TWIN'],['新文章','New post'],['插入模板…','Insert template…'],['查看本文 ↗','View this post ↗'],['查看','View'],['导入 .md','Import .md'],['插入图片','Insert image'],['用 Markdown 写作…','Write in Markdown…'],['支持 Markdown · 拖拽或粘贴图片到编辑框即可上传','Markdown supported · drop or paste images into the editor to upload'],['每篇文章都是公开的，会同时进入 Digital Twin 的知识库与记忆；编辑后会用新正文重建知识库索引。','Each post is public and feeds both your Digital Twin knowledge base and memories. Editing a post re-indexes the knowledge base with the new text.'],['写作沉淀为记忆','Writing becomes memory'],['写作沉淀为知识','Writing that becomes knowledge'],['每篇文章都会进入知识库并抽取为记忆，帮助 Echo of CSQ 理解我持续变化的想法。','Every published post is indexed into the knowledge base and distilled into memories, helping Echo of CSQ understand my evolving ideas.'],['与我的分身对话 →','Talk to my twin →'],['公开文章会同时进入知识库与记忆。','Public writing that becomes knowledge and memory'],['写作、知识库与记忆','Writing, knowledge and memory'],['文章与技术笔记会同时进入 Digital Twin 的知识库和长期记忆。','Essays and technical notes that teach my Digital Twin through knowledge indexing and memory extraction.'],['阅读 Blogs →','Read Blogs →'],['写作 → 学习','WRITE → LEARN'],
    ['从记忆中学习','Learned from memories'],['每篇公开文章都会同时更新 Digital Twin 的知识库与记忆。','Each post is public and feeds both your Digital Twin knowledge base and memories.'],['查看公开文章','View public posts'],['发布失败','Publish failed'],['重建失败','Rebuild failed'],['排队中','Queued'],['消化中…','Learning…'],['索引中…','Indexing…'],['失败','Failed'],['记忆学习','Memory'],['近期情绪','Recent mood'],['公开文章','Public posts'],['写作','WRITE'],['学习','LEARN'],
    // 站点装饰性标签：全大写的小字标题、模块编号与页脚。
    ['个人网站','personal website'],['CSQ — AI 全栈工程师','CSQ — AI FULL-STACK ENGINEER'],['四个模块，同一个首页','FOUR MODULES, ONE HOME'],['精选项目','SELECTED PROJECTS'],['个人知识 × 记忆','PERSONAL KNOWLEDGE × MEMORY'],['关注','FOCUS'],
    ['01 / 数字分身','01 / DIGITAL TWIN'],['02 / 趋势','02 / TRENDS'],['03 / 博客','03 / BLOGS'],['04 / 工具','04 / UTILS'],['03 / 开发者工具','03 / DEVELOPER UTILITIES'],['© csq · 开发者工具','© csq · Utils Developer Utilities'],['长期趋势','LONG-TERM TRENDS'],
    ['在浏览器中对比','Compared in your browser'],['A · 修改前','A · Before'],['B · 修改后','B · After'],['使用格式化输入','Use formatted input'],['已格式化','Formatted'],['在每个输入框粘贴 JSON 响应，然后选择「对比」。','Paste a JSON response in each input, then select Compare.'],['准备好后即可对比','Ready to compare'],['写作沉淀为知识与记忆','Writing becomes knowledge and memory'],['请求头','Headers'],['认证方式','Auth'],['导入 cURL 命令','Import cURL command'],['请求历史记录','Request history'],['发送','Send'],['移除','Remove'],['添加请求头','Add Header'],['值','Value'],['键','Key']
  ];
  const maps = {en: new Map(), zh: new Map()};
  for (const [zh, en] of pairs) {
    maps.en.set(zh, en); maps.en.set(en, en);
    maps.zh.set(en, zh); maps.zh.set(zh, zh);
  }
  const ordered = {en: [...maps.en.keys()].sort((a,b) => b.length-a.length), zh: [...maps.zh.keys()].sort((a,b) => b.length-a.length)};
  const STORAGE_KEY = 'csq-language';
  let language = 'en';
  try { if (localStorage.getItem(STORAGE_KEY) === 'zh') language = 'zh'; } catch (_) {}
  let observer;
  const skipSelector = 'script,style,code,pre,textarea,[data-no-translate],.bubble,.source-text,.profile .v,.company-profile-grid p,.blog-content,.article-content,.upcoming-earnings-sources a,.weekly-price-source,.block-source';
  function translate(value, target = language) {
    if (!value) return value;
    const leading = value.match(/^\s*/)[0], trailing = value.match(/\s*$/)[0];
    const core = value.trim();
    if (!core) return value;
    const map = maps[target];
    if (map.has(core)) return leading + map.get(core) + trailing;
    let out = core;
    for (const source of ordered[target]) {
      if (source.length < 2 || !out.includes(source)) continue;
      const ascii = /^[\x20-\x7E]+$/.test(source);
      if (target === 'en' && ascii) continue;
      if (target === 'zh' && ascii) {
        const escaped = source.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        out = out.replace(new RegExp('(?<![A-Za-z0-9])' + escaped + '(?![A-Za-z0-9])', 'g'), map.get(source));
      } else {
        out = out.split(source).join(map.get(source));
      }
    }
    out = out.replace(/\bWeUtil\b/g, 'Utils');
    return leading + out + trailing;
  }
  function translateElement(el) {
    if (!(el instanceof Element) || el.matches(skipSelector) || el.closest(skipSelector)) return;
    for (const attr of ['placeholder','title','aria-label']) {
      if (el.hasAttribute(attr)) {
        const next = translate(el.getAttribute(attr));
        if (el.getAttribute(attr) !== next) el.setAttribute(attr, next);
      }
    }
  }
  function translateTree(root = document) {
    if (root instanceof Element) translateElement(root);
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    for (const node of nodes) {
      const parent = node.parentElement;
      if (!parent || parent.closest(skipSelector)) continue;
      const next = translate(node.nodeValue);
      if (next !== node.nodeValue) node.nodeValue = next;
    }
    if (root === document) document.querySelectorAll('[placeholder],[title],[aria-label]').forEach(translateElement);
  }
  const items = [
    {path:'/', match:p => p === '/', en:'Home', zh:'首页'},
    {path:'/twin/', match:p => p.startsWith('/twin'), en:'Digital Twin', zh:'数字分身'},
    {path:'/trends', match:p => p.startsWith('/trends'), en:'Trends', zh:'趋势'},
    {path:'/blogs/', match:p => p.startsWith('/blogs'), en:'Blogs', zh:'博客'},
    {path:'/utils/', match:p => p.startsWith('/utils') || p.startsWith('/tools'), en:'Utils', zh:'工具'}
  ];
  function renderNav() {
    const nav = document.querySelector('.site-nav');
    if (!nav) return;
    const path = location.pathname;
    nav.setAttribute('aria-label', language === 'zh' ? '全站主导航' : 'Main navigation');
    nav.replaceChildren();
    for (const item of items) {
      const link = document.createElement('a'); link.href = item.path; link.textContent = item[language];
      if (item.match(path)) { link.className = 'active'; link.setAttribute('aria-current','page'); }
      nav.appendChild(link);
    }
    let button = document.querySelector('.site-language');
    if (!button) {
      button = document.createElement('button'); button.type = 'button'; button.className = 'site-language';
      nav.after(button);
      button.addEventListener('click', () => setLanguage(language === 'en' ? 'zh' : 'en'));
    }
    button.textContent = language === 'en' ? '中文' : 'EN';
    button.setAttribute('aria-label', language === 'en' ? 'Switch to Chinese' : '切换为英文');
  }
  function apply(root = document) {
    document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en';
    renderNav(); translateTree(root);
  }
  function setLanguage(next) {
    language = next;
    try { localStorage.setItem(STORAGE_KEY, next); } catch (_) {}
    apply();
    window.dispatchEvent(new CustomEvent('csq:languagechange', {detail:{language}}));
  }
  window.CSQ_I18N = {t: translate, language: () => language, apply: () => apply()};
  const initialize = () => {
    apply();
    window.dispatchEvent(new CustomEvent('csq:languagechange', {detail:{language}}));
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', initialize, {once:true}); else initialize();
  observer = new MutationObserver(mutations => {
    if (!document.querySelector('.site-nav')) return;
    for (const mutation of mutations) {
      for (const node of mutation.addedNodes) {
        if (node.nodeType === Node.TEXT_NODE) {
          const next = translate(node.nodeValue);
          if (next !== node.nodeValue) node.nodeValue = next;
        } else if (node.nodeType === Node.ELEMENT_NODE) translateTree(node);
      }
    }
  });
  observer.observe(document.documentElement, {childList:true,subtree:true});
})();
