/* 顾清影 · 演示数据（项目文件样例） */
(() => {
  'use strict';
  const G = (window.GQY = window.GQY || {});

  const FILES_MOCK = [
    {
      name: 'train.py', lang: 'python',
      content: [
        'from torch.utils.data import DataLoader',
        'from dataset import ImageFolderDataset, random_split',
        '',
        'def build(batch_size=32):',
        '    dataset = ImageFolderDataset("data/train", transform=transform)',
        '    train_set, val_set = random_split(dataset, [0.8, 0.2])',
        '    train_loader = DataLoader(train_set, batch_size=batch_size, shuffle=True, num_workers=4)',
        '    val_loader = DataLoader(val_set, batch_size=64)',
        '    return train_loader, val_loader',
        '',
        'if __name__ == "__main__":',
        '    train_loader, val_loader = build()',
        '    print(len(train_loader), len(val_loader))'
      ].join('\n')
    },
    {
      name: 'README.md', lang: 'md',
      content: [
        '# vision',
        '',
        '按文件夹分类的图片数据集，训练 / 验证按 8:2 拆分。',
        '',
        '- `dataset.py`：目录扫描与样本读取',
        '- `train.py`：DataLoader 与训练入口',
        '- `data/train/`：每个子目录是一个类别',
        '',
        '> 大数据集建议先生成 `index.json`，再按索引惰性加载。'
      ].join('\n')
    }
  ];

  G.FILES_MOCK = FILES_MOCK;
})();
