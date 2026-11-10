import * as echarts from 'echarts/core';
import { BarChart, LineChart } from 'echarts/charts';
import { GridComponent, LegendComponent, MarkAreaComponent, MarkLineComponent, TooltipComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';

// Tree-shaken: only the parts the charts use.
echarts.use([LineChart, BarChart, GridComponent, TooltipComponent, MarkAreaComponent, MarkLineComponent, LegendComponent, CanvasRenderer]);

export { echarts };
