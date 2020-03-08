#!/usr/bin/env python3
# vim:fileencoding=utf-8
# License: GPL v3 Copyright: 2018, Kovid Goyal <kovid at kovidgoyal.net>

import re
from functools import partial
from typing import (
    Any, Dict, Iterable, List, Optional, Set, Tuple, Union, get_type_hints
)

from .utils import to_bool


def to_string(x: str) -> str:
    return x


class Group:

    __slots__ = 'name', 'short_text', 'start_text', 'end_text'

    def __init__(self, name, short_text, start_text='', end_text=''):
        self.name, self.short_text = name, short_text.strip()
        self.start_text, self.end_text = start_text.strip(), end_text.strip()


class Option:

    __slots__ = 'name', 'group', 'long_text', 'option_type', 'defval_as_string', 'add_to_default', 'add_to_docs', 'line'

    def __init__(self, name: str, group: str, defval: str, option_type: Any, long_text: str, add_to_default: bool, add_to_docs: bool):
        self.name, self.group = name, group
        self.long_text, self.option_type = long_text.strip(), option_type
        self.defval_as_string = defval
        self.add_to_default = add_to_default
        self.add_to_docs = add_to_docs
        self.line = self.name + ' ' + self.defval_as_string

    def type_definition(self, is_multiple: bool, imports: Set[Tuple[str, str]]) -> str:

        def type_name(x: type) -> str:
            ans = x.__name__
            if x.__module__ and x.__module__ != 'builtins':
                imports.add((x.__module__, x.__name__))
            if is_multiple:
                ans = 'typing.Dict[str, str]'
            return ans

        def option_type_as_str(x: Any) -> str:
            if hasattr(x, '__name__'):
                return type_name(x)
            ans = repr(x)
            ans = ans.replace('NoneType', 'None')
            return ans

        if type(self.option_type) is type:
            return type_name(self.option_type)
        th = get_type_hints(self.option_type)
        try:
            rettype = th['return']
        except KeyError:
            raise ValueError('The Option {} has an unknown option_type: {}'.format(self.name, self.option_type))
        return option_type_as_str(rettype)


class Shortcut:

    __slots__ = 'name', 'group', 'key', 'action_def', 'short_text', 'long_text', 'add_to_default', 'add_to_docs', 'line'

    def __init__(self, name, group, key, action_def, short_text, long_text, add_to_default, add_to_docs):
        self.name, self.group, self.key, self.action_def = name, group, key, action_def
        self.short_text, self.long_text = short_text, long_text
        self.add_to_default = add_to_default
        self.add_to_docs = add_to_docs
        self.line = 'map ' + self.key + ' ' + self.action_def


def option(
    all_options,
    group,
    name,
    defval,
    long_text='',
    option_type=to_string,
    add_to_default=True,
    add_to_docs=True
):
    is_multiple = name.startswith('+')
    if is_multiple:
        name = name[1:]
    defval_type = type(defval)
    if defval_type is not str:
        if option_type is to_string:
            if defval_type is bool:
                option_type = to_bool
            else:
                option_type = defval_type
        if defval_type is bool:
            defval = 'yes' if defval else 'no'
        else:
            defval = str(defval)

    key = name
    if is_multiple:
        key = name + ' ' + defval.partition(' ')[0]
    ans = Option(name, group[0], defval, option_type, long_text, add_to_default, add_to_docs)
    all_options[key] = ans
    return ans


def shortcut(
    all_options,
    group,
    action_name,
    key,
    action_def,
    short_text='',
    long_text='',
    add_to_default=True,
    add_to_docs=True
):
    ans = Shortcut(action_name, group[0], key, action_def, short_text, long_text, add_to_default, add_to_docs)
    key = 'sc-' + action_name
    all_options.setdefault(key, []).append(ans)
    return ans


def option_func(all_options, all_groups):
    all_groups = {k: Group(k, *v) for k, v in all_groups.items()}
    group = [None]

    def change_group(name):
        group[0] = all_groups[name]

    return partial(option, all_options, group), partial(shortcut, all_options, group), change_group, all_groups


def merged_opts(all_options, opt, i):
    yield opt
    for k in range(i + 1, len(all_options)):
        q = all_options[k]
        if not isinstance(q, Option):
            break
        if not q.long_text and q.add_to_docs:
            yield q
        else:
            break


def remove_markup(text):

    def sub(m):
        if m.group(1) == 'ref':
            return {
                'layouts': 'https://sw.kovidgoyal.net/kitty/index.html#layouts',
                'sessions': 'https://sw.kovidgoyal.net/kitty/index.html#sessions',
            }[m.group(2)]
        return m.group(2)

    return re.sub(r':([a-zA-Z0-9]+):`(.+?)`', sub, text, flags=re.DOTALL)


def iter_blocks(lines: Iterable[str]):
    current_block: List[str] = []
    prev_indent = 0
    for line in lines:
        indent_size = len(line) - len(line.lstrip())
        if indent_size != prev_indent or not line:
            if current_block:
                yield current_block, prev_indent
            current_block = []
        prev_indent = indent_size
        if not line:
            yield [''], 100
        else:
            current_block.append(line)
    if current_block:
        yield current_block, indent_size


def wrapped_block(lines):
    wrapper = getattr(wrapped_block, 'wrapper', None)
    if wrapper is None:
        import textwrap
        wrapper = textwrap.TextWrapper(
            initial_indent='#: ', subsequent_indent='#: ', width=70, break_long_words=False
        )
        setattr(wrapped_block, 'wrapper', wrapper)
    for block, indent_size in iter_blocks(lines):
        if indent_size > 0:
            for line in block:
                if not line:
                    yield line
                else:
                    yield '#: ' + line
        else:
            for line in wrapper.wrap('\n'.join(block)):
                yield line


def render_block(text):
    text = remove_markup(text)
    lines = text.splitlines()
    return '\n'.join(wrapped_block(lines))


def as_conf_file(all_options: Iterable[Union[Option, Shortcut]]) -> List[str]:
    ans = ['# vim:fileencoding=utf-8:ft=conf:foldmethod=marker', '']
    a = ans.append
    current_group: Optional[Group] = None
    num_open_folds = 0
    all_options = list(all_options)

    def render_group(group, is_shortcut):
        nonlocal num_open_folds
        if is_shortcut or '.' not in group.name:
            a('#: ' + group.short_text + ' {{''{')
            num_open_folds += 1
        a('')
        if group.start_text:
            a(render_block(group.start_text))
            a('')

    def handle_group_end(group, new_group_name='', new_group_is_shortcut=False):
        nonlocal num_open_folds
        if group.end_text:
            a(''), a(render_block(group.end_text))
        is_subgroup = new_group_name.startswith(group.name + '.')
        if not is_subgroup and num_open_folds > 0:
            a('#: }}''}'), a('')
            num_open_folds -= 1

    def handle_group(new_group, is_shortcut=False):
        nonlocal current_group
        if new_group is not current_group:
            if current_group:
                handle_group_end(current_group, new_group.name, is_shortcut)
            current_group = new_group
            render_group(current_group, is_shortcut)

    def handle_shortcut(shortcuts):
        handle_group(shortcuts[0].group, True)
        for sc in shortcuts:
            if sc.add_to_default:
                a('map {} {}'.format(sc.key, sc.action_def))
            if sc.long_text:
                a(''), a(render_block(sc.long_text.strip())), a('')

    def handle_option(opt):
        if not opt.long_text or not opt.add_to_docs:
            return
        handle_group(opt.group)
        mopts = list(merged_opts(all_options, opt, i))
        sz = max(len(x.name) for x in mopts)
        for mo in mopts:
            prefix = '' if mo.add_to_default else '# '
            a('{}{} {}'.format(prefix, mo.name.ljust(sz), mo.defval_as_string))
        a('')
        a(render_block(opt.long_text))
        a('')

    for i, opt in enumerate(all_options):
        if isinstance(opt, Option):
            handle_option(opt)
        else:
            handle_shortcut(opt)

    if current_group:
        handle_group_end(current_group)
        while num_open_folds > 0:
            a('# }}''}')
            num_open_folds -= 1

    map_groups = []
    start: Optional[int] = None
    count: Optional[int] = None
    for i, line in enumerate(ans):
        if line.startswith('map '):
            if start is None:
                start = i
                count = 1
            else:
                if count is not None:
                    count += 1
        else:
            if start is not None and count is not None:
                map_groups.append((start, count))
                start = count = None
    for start, count in map_groups:
        r = range(start, start + count)
        sz = max(len(ans[i].split(' ', 3)[1]) for i in r)
        for i in r:
            line = ans[i]
            parts = line.split(' ', 3)
            parts[1] = parts[1].ljust(sz)
            ans[i] = ' '.join(parts)

    return ans


def config_lines(all_options):
    for opt in all_options.values():
        if isinstance(opt, Option):
            if opt.add_to_default:
                yield opt.line
        else:
            for sc in opt:
                if sc.add_to_default:
                    yield sc.line


def as_type_stub(
    all_options: Dict[str, Union[Option, List[Shortcut]]],
    special_types: Optional[Dict[str, str]] = None,
    preamble_lines: Union[Tuple[str, ...], List[str], Iterable[str]] = (),
    extra_fields: Union[Tuple[Tuple[str, str], ...], List[Tuple[str, str]], Iterable[Tuple[str, str]]] = (),
    class_name: str = 'Options'
) -> str:
    ans = ['import typing\n'] + list(preamble_lines) + ['', 'class {}:'.format(class_name)]
    imports: Set[Tuple[str, str]] = set()
    overrides = special_types or {}
    for name, val in all_options.items():
        if isinstance(val, Option):
            is_multiple = ' ' in name
            field_name = name.partition(' ')[0]
            ans.append('    {}: {}'.format(field_name, overrides.get(field_name, val.type_definition(is_multiple, imports))))
    for mod, name in imports:
        ans.insert(0, 'from {} import {}'.format(mod, name))
        ans.insert(0, 'import {}'.format(mod))
    for field_name, type_def in extra_fields:
        ans.append('    {}: {}'.format(field_name, type_def))
    ans.append('    def __iter__(self): pass')
    ans.append('    def __len__(self): pass')
    ans.append('    def _replace(self, **kw) -> {}: pass'.format(class_name))
    return '\n'.join(ans) + '\n\n\n'


def save_type_stub(text: str, fpath: str) -> None:
    import os
    fpath += 'i'
    preamble = '# Update this file by running: python {}\n\n'.format(os.path.relpath(os.path.abspath(fpath)))
    try:
        existing = open(fpath).read()
    except FileNotFoundError:
        existing = ''
    current = preamble + text
    if existing != current:
        open(fpath, 'w').write(current)
