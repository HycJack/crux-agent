# -*- coding: utf-8 -*-
"""
冒泡排序 (Bubble Sort)
时间复杂度: O(n^2)
空间复杂度: O(1)
"""


def bubble_sort(arr):
    """对列表进行冒泡排序（升序）"""
    n = len(arr)
    for i in range(n - 1):
        swapped = False
        # 每次循环把当前最大的元素"冒"到末尾
        for j in range(0, n - 1 - i):
            if arr[j] > arr[j + 1]:
                arr[j], arr[j + 1] = arr[j + 1], arr[j]
                swapped = True
        # 如果这一轮没有发生交换，说明已经有序，可以提前结束
        if not swapped:
            break
    return arr


if __name__ == "__main__":
    # 测试数据
    test_cases = [
        [64, 34, 25, 12, 22, 11, 90],
        [5, 1, 4, 2, 8],
        [1, 2, 3, 4, 5],          # 已经有序
        [5, 4, 3, 2, 1],          # 逆序
        [3],                       # 单个元素
        [],                        # 空列表
        [2, 2, 2, 1, 1],          # 含重复元素
    ]

    for idx, data in enumerate(test_cases, 1):
        original = data.copy()
        sorted_data = bubble_sort(data.copy())
        print(f"用例 {idx}:")
        print(f"  原始数组: {original}")
        print(f"  排序结果: {sorted_data}")
        print()
