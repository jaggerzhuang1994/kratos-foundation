#!/usr/bin/env python3
"""按 Git tag 重新生成 CHANGELOG.md 中每个版本的“提交记录”块。

- 版本取 v2 正式 tag（忽略 -rc 等预发布），范围为上一个正式 tag 到当前 tag；
  首个 v2 版本从最近的 v1 tag 开始。
- 手写内容（升级风险、Added、Changed 等）保持原样，只替换标记之间的自动块，可重复执行。
- 尚未打 tag 的提交写入“计划中”节；打 tag 前用 `--release vX.Y.Z`（make changelog RELEASE=vX.Y.Z）
  把“计划中”的手写说明与未发布提交整理为该版本，并留空“计划中”。
提交按 Conventional Commits 类型分组，`type!:` 标记为不兼容变更。
"""
import argparse
import re
import subprocess
from datetime import date
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
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--release', help='打 tag 前整理发布说明：把“计划中”与未发布提交归入该版本（如 v2.5.0）')
    args = parser.parse_args()
    head, manual = split_sections(CHANGELOG.read_text())
    tags = releases()
    release = args.release
    if release:
        if not re.fullmatch(r'v\d+\.\d+\.\d+', release):
            raise SystemExit(f'--release 须为 vX.Y.Z：{release}')
        if release in tags:
            raise SystemExit(f'{release} 已存在 tag，直接运行 make changelog 即可')
        if tags and version_key(release) <= version_key(tags[-1]):
            raise SystemExit(f'{release} 必须大于最新版本 {tags[-1]}')
    # 没有对应 tag 的版本节只允许是本次 --release 的目标，否则其手写说明会被静默丢弃。
    orphans = sorted(k for k in manual if k != UNRELEASED and k not in tags and k != release)
    if orphans:
        raise SystemExit(f'以下版本节没有对应 tag：{", ".join(orphans)}；打 tag 后再运行，或使用 --release 指定目标版本')
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
    if release:
        # 计划中的手写说明随本次发布移入版本节，计划中留空供后续开发继续记录。
        notes = manual.get(release) or manual.get(UNRELEASED, '')
        parts.append(render([]) + '\n')
        parts.append(f'## {release}（{date.today().isoformat()}）\n')
        if notes:
            parts.append(notes + '\n')
        parts.append(render(unreleased) + '\n')
    else:
        if manual.get(UNRELEASED):
            parts.append(manual[UNRELEASED] + '\n')
        parts.append(render(unreleased) + '\n')
    for tag in reversed(tags):
        tagged = git('log', '-1', '--format=%cs', tag).strip()
        parts.append(f'## {tag}（{tagged}）\n')
        if manual.get(tag):
            parts.append(manual[tag] + '\n')
        parts.append(render(commits(previous[tag], tag)) + '\n')
    CHANGELOG.write_text('\n'.join(parts).rstrip('\n') + '\n')
    target = f'，发布版本 {release}' if release else ''
    print(f'CHANGELOG.md 已更新：{len(tags)} 个已发布版本，未发布提交 {len(unreleased)} 个{target}')


if __name__ == '__main__':
    main()
