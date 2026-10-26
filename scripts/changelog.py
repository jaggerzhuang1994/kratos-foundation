#!/usr/bin/env python3
"""按 Git tag 重新生成 CHANGELOG.md 中每个版本的“提交记录”块。

- 版本取 v2 正式 tag（忽略 -rc 等预发布），范围为上一个正式 tag 到当前 tag；
  首个 v2 版本从最近的 v1 tag 开始。
- 手写内容（升级风险、Added、Changed 等）保持原样，只替换标记之间的自动块，可重复执行。
- 尚未打 tag 的提交写入“计划中”节。
提交按 Conventional Commits 类型分组，`type!:` 标记为不兼容变更。
"""
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CHANGELOG = ROOT / 'CHANGELOG.md'
UNRELEASED = '计划中'
GROUPS = [('feat', '新增'), ('fix', '修复'), ('perf', '性能'), ('refactor', '重构'),
          ('docs', '文档'), ('test', '测试'), ('build', '构建与依赖'), ('chore', '其他')]
SUBJECT = re.compile(r'^(?P<type>\w+)(?:\((?P<scope>[^)]*)\))?(?P<breaking>!)?:\s*(?P<text>.+)$')
HEADING = re.compile(r'^## (?P<key>v\d+\.\d+\.\d+|' + UNRELEASED + r')\b.*$', re.M)
BLOCK = re.compile(r'\n*<!-- changelog:auto:start -->.*?<!-- changelog:auto:end -->\n*', re.S)


def git(*args):
    return subprocess.run(['git', *args], cwd=ROOT, check=True, capture_output=True, text=True).stdout


def version_key(tag):
    return tuple(int(part) for part in tag[1:].split('.'))


def releases():
    tags = [t for t in git('tag', '--list', 'v2.*').split() if re.fullmatch(r'v\d+\.\d+\.\d+', t)]
    return sorted(tags, key=version_key)


def commits(start, end):
    span = f'{start}..{end}' if start else end
    lines = git('log', '--no-merges', '--format=%h%x09%s', span).splitlines()
    return [line.split('\t', 1) for line in lines if line]


def render(entries):
    """渲染一个版本的提交记录；无提交时说明版本只包含 tag 本身。"""
    grouped = {name: [] for _, name in GROUPS}
    for sha, subject in entries:
        match = SUBJECT.match(subject)
        kind = match['type'] if match else 'chore'
        name = dict(GROUPS).get(kind, '其他')
        text = match['text'] if match else subject
        scope = f"**{match['scope']}**：" if match and match['scope'] else ''
        mark = '⚠️ 不兼容：' if match and match['breaking'] else ''
        grouped[name].append(f'- {mark}{scope}{text}（`{sha}`）')
    lines = ['<!-- changelog:auto:start -->', '### 提交记录', '',
             '> 由 `make changelog` 按 Git tag 生成，请勿手改本块。', '']
    if not entries:
        lines.append('- 无新增提交')
    for _, name in GROUPS:
        if grouped[name]:
            lines += [f'**{name}**', ''] + grouped[name] + ['']
    return '\n'.join(lines).rstrip() + '\n<!-- changelog:auto:end -->'


def split_sections(text):
    """把文件拆为头部和按版本归类的手写正文；未识别的标题归入前一节。"""
    matches = list(HEADING.finditer(text))
    head = text[:matches[0].start()] if matches else text
    sections = {}
    for index, match in enumerate(matches):
        end = matches[index + 1].start() if index + 1 < len(matches) else len(text)
        body = text[match.end():end]
        sections[match['key']] = BLOCK.sub('\n\n', body).strip('\n')
    return head.rstrip('\n'), sections


def main():
    head, manual = split_sections(CHANGELOG.read_text())
    tags = releases()
    previous = {}
    for index, tag in enumerate(tags):
        if index:
            previous[tag] = tags[index - 1]
        else:
            # 首个 v2 版本承接 v1 历史，从最近的 v1 tag 开始统计。
            previous[tag] = git('describe', '--tags', '--abbrev=0', '--match', 'v1.*', f'{tag}^').strip()
    parts = [head, '']
    unreleased = commits(tags[-1], 'HEAD') if tags else commits(None, 'HEAD')
    parts.append(f'## {UNRELEASED}\n')
    if manual.get(UNRELEASED):
        parts.append(manual[UNRELEASED] + '\n')
    parts.append(render(unreleased) + '\n')
    for tag in reversed(tags):
        date = git('log', '-1', '--format=%cs', tag).strip()
        parts.append(f'## {tag}（{date}）\n')
        if manual.get(tag):
            parts.append(manual[tag] + '\n')
        parts.append(render(commits(previous[tag], tag)) + '\n')
    CHANGELOG.write_text('\n'.join(parts).rstrip('\n') + '\n')
    print(f'CHANGELOG.md 已更新：{len(tags)} 个版本，未发布提交 {len(unreleased)} 个')


if __name__ == '__main__':
    main()
