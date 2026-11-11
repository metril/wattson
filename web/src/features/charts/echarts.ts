import * as echarts from 'echarts/core';
import { BarChart, LineChart } from 'echarts/charts';
import { DataZoomComponent, GridComponent, LegendComponent, MarkAreaComponent, MarkLineComponent, ToolboxComponent, TooltipComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';

// Tree-shaken: only the parts the charts use.
echarts.use([LineChart, BarChart, GridComponent, TooltipComponent, MarkAreaComponent, MarkLineComponent, LegendComponent, DataZoomComponent, ToolboxComponent, CanvasRenderer]);

export { echarts };
